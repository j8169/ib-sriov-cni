package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types"
)

var (
	sriovConfigured = "/sriov_numvfs"
	// NetDirectory sysfs net directory
	NetDirectory = "/sys/class/net"
	// SysBusPci is sysfs pci device directory
	SysBusPci = "/sys/bus/pci/devices"
)

const (
	IPoIBAddrLengthBytes       = 20
	OwnerReadWriteExecuteAttrs = 0700
	OwnerReadWriteAttrs        = 0600
	VfioPciDriverName          = "vfio-pci"
	DefaultGUID                = "FF:FF:FF:FF:FF:FF:FF:FF"
)

// GetSriovNumVfs takes in a PF name(ifName) as string and returns number of VF configured as int
func GetSriovNumVfs(ifName string) (int, error) {
	var vfTotal int

	sriovFile := filepath.Join(NetDirectory, ifName, "device", sriovConfigured)
	if _, err := os.Lstat(sriovFile); err != nil {
		return vfTotal, fmt.Errorf("failed to open the sriov_numfs of device %q: %v", ifName, err)
	}

	data, err := os.ReadFile(sriovFile) /* #nosec G304 */
	if err != nil {
		return vfTotal, fmt.Errorf("failed to read the sriov_numfs of device %q: %v", ifName, err)
	}

	if len(data) == 0 {
		return vfTotal, fmt.Errorf("no data in the file %q", sriovFile)
	}

	sriovNumfs := strings.TrimSpace(string(data))
	vfTotal, err = strconv.Atoi(sriovNumfs)
	if err != nil {
		return vfTotal, fmt.Errorf("failed to convert sriov_numfs(byte value) to int of device %q: %v", ifName, err)
	}

	return vfTotal, nil
}

// GetVfid takes in VF's PCI address(addr) and pfName as string and returns VF's ID as int
func GetVfid(addr, pfName string) (int, error) {
	var id int
	vfTotal, err := GetSriovNumVfs(pfName)
	if err != nil {
		return id, err
	}
	for vf := 0; vf <= vfTotal; vf++ {
		vfDir := filepath.Join(NetDirectory, pfName, "device", fmt.Sprintf("virtfn%d", vf))
		_, err := os.Lstat(vfDir)
		if err != nil {
			continue
		}
		pciinfo, err := os.Readlink(vfDir)
		if err != nil {
			continue
		}
		pciaddr := filepath.Base(pciinfo)
		if pciaddr == addr {
			return vf, nil
		}
	}
	return id, fmt.Errorf("unable to get VF ID with PF: %s and VF pci address %v", pfName, addr)
}

// GetPfName returns PF net device name of a given VF pci address
func GetPfName(vf string) (string, error) {
	pfSymLink := filepath.Join(SysBusPci, vf, "physfn", "net")
	_, err := os.Lstat(pfSymLink)
	if err != nil {
		return "", err
	}

	files, err := os.ReadDir(pfSymLink)
	if err != nil {
		return "", err
	}

	if len(files) < 1 {
		return "", fmt.Errorf("PF network device not found")
	}

	return strings.TrimSpace(files[0].Name()), nil
}

// GetPciAddress takes in a interface(ifName) and VF id and returns returns its pci addr as string
func GetPciAddress(ifName string, vf int) (string, error) {
	var pciaddr string
	vfDir := filepath.Join(NetDirectory, ifName, "device", fmt.Sprintf("virtfn%d", vf))
	dirInfo, err := os.Lstat(vfDir)
	if err != nil {
		return pciaddr, fmt.Errorf("can't get the symbolic link of virtfn%d dir of the device %q: %v", vf, ifName, err)
	}

	if (dirInfo.Mode() & os.ModeSymlink) == 0 {
		return pciaddr, fmt.Errorf("no symbolic link for the virtfn%d dir of the device %q", vf, ifName)
	}

	pciinfo, err := os.Readlink(vfDir)
	if err != nil {
		return pciaddr, fmt.Errorf("can't read the symbolic link of virtfn%d dir of the device %q: %v", vf, ifName, err)
	}

	pciaddr = filepath.Base(pciinfo)
	return pciaddr, nil
}

// UdevDataDir is where systemd-udevd records devices it has finished processing; a network
// device's record is "n<ifindex>". Variable so tests can redirect it.
var UdevDataDir = "/run/udev/data"

// withDefaults fills zero timing fields (e.g. a NetdevWait{} literal) from types.DefaultNetdevWait.
func withDefaults(w types.NetdevWait) types.NetdevWait {
	d := types.DefaultNetdevWait
	if w.LookupAttempts <= 0 {
		w.LookupAttempts = d.LookupAttempts
	}
	if w.LookupInterval <= 0 {
		w.LookupInterval = d.LookupInterval
	}
	if w.UdevTimeout <= 0 {
		w.UdevTimeout = d.UdevTimeout
	}
	if w.UdevPoll <= 0 {
		w.UdevPoll = d.UdevPoll
	}
	return w
}

// udevNetdevSettled reads udev's database record for a network device and reports whether udev has
// finished with it. The record's existence is not enough: systemd-udevd writes it, with an "I:"
// (initialized) line and the property "E:ID_RENAMING=1", *before* it renames the interface, and only
// drops ID_RENAMING when it has processed the kernel's move uevent for the rename. So "settled" is:
// initialized, and not (or no longer) renaming. This mirrors sd_device_get_is_initialized plus the
// ID_RENAMING check systemd's own network code applies. Alt names are applied by the net_setup_link
// builtin during rule processing, before the first record is written, so they are in place too.
// A missing record means udev has not got to the device yet (not settled, no error); any other
// read error is reported.
//
// Coupling to systemd, stated plainly: the record format (one field per line, "I:" initialized
// timestamp, "E:KEY=VALUE" properties) is systemd-udevd's internal database, not a public API. The
// public interfaces, libsystemd's sd_device_get_is_initialized and sd_device_get_property_value for
// "ID_RENAMING", answer the same question by reading the same file, so using them would move the
// format dependency into a library at the cost of cgo or a glibc-dynamic binary, which this plugin
// and its siblings deliberately avoid (static CGO_ENABLED=0 binaries copied onto the host). The
// layout has been stable since udev 18x (2013) and ID_RENAMING since systemd v243 (2019); verified
// against systemd 255 (see the record-shape tests). Only two facts are read from the record and
// nothing else is interpreted: whether an "I:" line exists and whether the exact line
// "E:ID_RENAMING=1" exists. If a future systemd changes either, the wait times out and the ADD
// fails closed with ErrUdevTimeout rather than racing udev.
func udevNetdevSettled(record string) (bool, error) {
	data, err := os.ReadFile(record) /* #nosec G304 -- record is UdevDataDir + "n<ifindex>", ifindex is an integer */
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read udev record %s: %w", record, err)
	}
	initialized := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "I:"):
			initialized = true
		case line == "E:ID_RENAMING=1":
			return false, nil
		}
	}
	return initialized, nil
}

var (
	// ErrNoUdev is returned by WaitForUdevSettled when the host has no udev database at all.
	ErrNoUdev = errors.New("no udev database on this host")
	// ErrUdevTimeout is returned by WaitForUdevSettled when udev has not finished with the device in time.
	ErrUdevTimeout = errors.New("udev did not finish processing the network device in time")
)

// WaitForUdevSettled waits until udev has finished processing the network device with the given
// ifindex, so that its persistent name and alt names are in place and the caller does not race udev
// for the device's name (see udevNetdevSettled for what "finished" means). Returns nil when settled,
// ErrNoUdev at once when the udev database directory does not exist (nothing to wait for),
// ErrUdevTimeout after w.UdevTimeout, and any other filesystem error as is. Callers decide: the ADD
// path fails on a timeout (the runtime retries), the DEL path proceeds as it did before this wait.
func WaitForUdevSettled(ifIndex int, w types.NetdevWait) error {
	w = withDefaults(w)
	if _, err := os.Stat(UdevDataDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNoUdev
		}
		return fmt.Errorf("failed to access udev database %s: %w", UdevDataDir, err)
	}
	record := filepath.Join(UdevDataDir, fmt.Sprintf("n%d", ifIndex))
	deadline := time.Now().Add(w.UdevTimeout)
	for {
		settled, err := udevNetdevSettled(record)
		if err != nil {
			return err
		}
		if settled {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%w: ifindex %d, waited %s", ErrUdevTimeout, ifIndex, w.UdevTimeout)
		}
		// never sleep past the deadline, whatever the poll interval is configured to
		time.Sleep(min(w.UdevPoll, remaining))
	}
}

// GetVFLinkIndex returns the ifindex of the network interface of the VF at pciAddr.
// Unlike the name from GetVFLinkNames, the ifindex does not change when udev renames the
// interface, which it does concurrently with the CNI right after a VF rebind. Right after
// the rebind the netdev may not be registered yet (net/ missing or empty), and the entry under
// net/ is the current name, so a rename between listing it and reading its ifindex file gives
// ENOENT. Both are transient and retried within one budget; any other error is returned.
func GetVFLinkIndex(pciAddr string, w types.NetdevWait) (int, error) {
	w = withDefaults(w)
	vfDir := filepath.Join(SysBusPci, pciAddr, "net")
	var lastErr error
	for attempt := 0; attempt < w.LookupAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(w.LookupInterval)
		}
		entries, err := os.ReadDir(vfDir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			lastErr = fmt.Errorf("net dir of the device %s does not exist yet", pciAddr)
			continue
		case err != nil:
			return 0, fmt.Errorf("failed to read net dir of the device %s: %v", pciAddr, err)
		case len(entries) == 0:
			lastErr = fmt.Errorf("VF device %s sysfs path (%s) has no entries", pciAddr, vfDir)
			continue
		}
		data, err := os.ReadFile(filepath.Join(vfDir, entries[0].Name(), "ifindex")) /* #nosec G304 -- path under SysBusPci */
		if errors.Is(err, fs.ErrNotExist) {
			lastErr = err
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("failed to read ifindex of VF device %s: %v", pciAddr, err)
		}
		ifIndex, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return 0, fmt.Errorf("invalid ifindex for VF device %s: %v", pciAddr, err)
		}
		return ifIndex, nil
	}
	return 0, fmt.Errorf("failed to get ifindex of VF device %s: %v", pciAddr, lastErr)
}

// GetVFLinkNames returns VF's network interface name given it's PCI addr
func GetVFLinkNames(pciAddr string) (string, error) {
	vfDir := filepath.Join(SysBusPci, pciAddr, "net")
	if _, err := os.Lstat(vfDir); err != nil {
		return "", err
	}

	fInfos, err := os.ReadDir(vfDir)
	if err != nil {
		return "", fmt.Errorf("failed to read net dir of the device %s: %v", pciAddr, err)
	}

	if len(fInfos) == 0 {
		return "", fmt.Errorf("VF device %s sysfs path (%s) has no entries", pciAddr, vfDir)
	}

	names := make([]string, 0, len(fInfos))
	for _, f := range fInfos {
		names = append(names, f.Name())
	}

	return names[0], nil
}

// GetVFLinkNamesFromVFID returns VF's network interface name given it's PF name as string and VF id as int
func GetVFLinkNamesFromVFID(pfName string, vfID int) ([]string, error) {
	vfDir := filepath.Join(NetDirectory, pfName, "device", fmt.Sprintf("virtfn%d", vfID), "net")
	if _, err := os.Lstat(vfDir); err != nil {
		return nil, err
	}

	fInfos, err := os.ReadDir(vfDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read the virtfn%d dir of the device %q: %v", vfID, pfName, err)
	}

	names := make([]string, 0, len(fInfos))
	for _, f := range fInfos {
		names = append(names, f.Name())
	}

	return names, nil
}

// SaveNetConf takes in container ID, data dir and Pod interface name as string and a json encoded struct Conf
// and save this Conf in data dir
func SaveNetConf(cid, dataDir, podIfName string, conf interface{}) error {
	netConfBytes, err := json.Marshal(conf)
	if err != nil {
		return fmt.Errorf("error serializing delegate netconf: %v", err)
	}

	s := []string{cid, podIfName}
	cRef := strings.Join(s, "-")

	// save the rendered netconf for cmdDel
	err = saveScratchNetConf(cRef, dataDir, netConfBytes)
	if err != nil {
		return err
	}

	return nil
}

func saveScratchNetConf(containerID, dataDir string, netconf []byte) error {
	if err := os.MkdirAll(dataDir, OwnerReadWriteExecuteAttrs); err != nil {
		return fmt.Errorf("failed to create the sriov data directory(%q): %v", dataDir, err)
	}

	path := filepath.Join(dataDir, containerID)

	err := os.WriteFile(path, netconf, OwnerReadWriteAttrs)
	if err != nil {
		return fmt.Errorf("failed to write container data in the path(%q): %v", path, err)
	}

	return err
}

// ReadScratchNetConf takes in container ID, Pod interface name and data dir as string and returns a pointer to Conf
func ReadScratchNetConf(cRefPath string) ([]byte, error) {
	data, err := os.ReadFile(cRefPath) /* #nosec G304 */
	if err != nil {
		return nil, fmt.Errorf("failed to read container data in the path(%q): %v", cRefPath, err)
	}

	return data, err
}

// CleanCachedNetConf removed cached NetConf from disk
func CleanCachedNetConf(cRefPath string) error {
	if err := os.Remove(cRefPath); err != nil {
		return fmt.Errorf("error removing NetConf file %s: %q", cRefPath, err)
	}
	return nil
}

// Return GUID string, extracted from hardware address if it is IPoIB, or empty string
func GetGUIDFromHwAddr(hwAddr net.HardwareAddr) string {
	if len(hwAddr) == IPoIBAddrLengthBytes {
		// GUID is lower 8 bytes of hardware address
		return hwAddr[12:].String()
	}
	return ""
}

// IsValidGUID check if the guid is valid
func IsValidGUID(guid string) bool {
	if IsAllZeroGUID(guid) {
		return false
	}

	match, _ := regexp.MatchString("^([0-9a-fA-F]{2}:){7}[0-9a-fA-F]{2}$", guid)
	return match
}

// IsAllZeroGUID check if the guid is all zero which is invalid guid
func IsAllZeroGUID(guid string) bool {
	return guid == "00:00:00:00:00:00:00:00"
}

// IsAllOnesGUID check if the guid is all ones
func IsAllOnesGUID(guid string) bool {
	return guid == "ff:ff:ff:ff:ff:ff:ff:ff" || guid == "FF:FF:FF:FF:FF:FF:FF:FF"
}

// IsVfioPciDevice checks if a PCI device is bound to vfio-pci driver
func IsVfioPciDevice(pciAddr string) (bool, error) {
	driverPath := filepath.Join(SysBusPci, pciAddr, "driver")

	// Check if driver symlink exists
	linkTarget, err := os.Readlink(driverPath)
	if err != nil {
		// If readlink fails, the device might not be bound to any driver
		return false, nil
	}

	// Check if the driver is vfio-pci
	driverName := filepath.Base(linkTarget)
	return driverName == VfioPciDriverName, nil
}

// IsVirtualFunction checks if a PCI device is a VF by checking for physfn symlink
func IsVirtualFunction(pciAddr string) (bool, error) {
	physfnPath := filepath.Join(SysBusPci, pciAddr, "physfn")

	// Check if physfn symlink exists
	_, err := os.Lstat(physfnPath)
	if err != nil {
		if os.IsNotExist(err) {
			// physfn doesn't exist, so this is not a VF (likely a PF)
			return false, nil
		}
		// Other error occurred
		return false, err
	}

	// physfn exists, so this is a VF
	return true, nil
}
