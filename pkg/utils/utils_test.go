package utils

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types"
)

// Timing options for the tests: defaults, a short lookup budget for the negative GetVFLinkIndex cases,
// and a short udev wait for the timeout cases.
var (
	defaults    = types.NetdevWait{}
	shortLookup = types.NetdevWait{LookupAttempts: 3, LookupInterval: time.Millisecond}
	shortUdev   = types.NetdevWait{UdevTimeout: 150 * time.Millisecond, UdevPoll: 5 * time.Millisecond}
)

var _ = Describe("Utils", func() {

	Context("Checking GetSriovNumVfs function", func() {
		It("Assuming existing interface", func() {
			result, err := GetSriovNumVfs("ib0")
			Expect(result).To(Equal(2), "Existing sriov interface should return correct VFs count")
			Expect(err).NotTo(HaveOccurred(), "Existing sriov interface should not return an error")
		})
		It("Assuming not existing interface", func() {
			_, err := GetSriovNumVfs("enp175s0f2")
			Expect(err).To(HaveOccurred(), "Not existing sriov interface should return an error")
		})
	})
	Context("Checking GetVfid function", func() {
		It("Assuming existing interface", func() {
			result, err := GetVfid("0000:af:06.0", "ib0")
			Expect(result).To(Equal(0), "Existing VF should return correct VF index")
			Expect(err).NotTo(HaveOccurred(), "Existing VF should not return an error")
		})
		It("Assuming not existing interface", func() {
			_, err := GetVfid("0000:af:06.0", "enp175s0f2")
			Expect(err).To(HaveOccurred(), "Not existing interface should return an error")
		})
	})
	Context("Checking GetPfName function", func() {
		It("Assuming existing vf", func() {
			result, err := GetPfName("0000:af:06.0")
			Expect(err).NotTo(HaveOccurred(), "Existing VF should not return an error")
			Expect(result).To(Equal("ib0"), "Existing VF should return correct PF name")
		})
		It("Assuming not existing vf", func() {
			result, err := GetPfName("0000:af:07.0")
			Expect(result).To(Equal(""))
			Expect(err).To(HaveOccurred(), "Not existing VF should return an error")
		})
	})
	Context("Checking GetPciAddress function", func() {
		It("Assuming existing interface and vf", func() {
			Expect(GetPciAddress("ib0", 0)).To(Equal("0000:af:06.0"),
				"Existing PF and VF id should return correct VF pci address")
		})
		It("Assuming not existing interface", func() {
			_, err := GetPciAddress("enp175s0f2", 0)
			Expect(err).To(HaveOccurred(), "Not existing PF should return an error")
		})
		It("Assuming not existing vf", func() {
			result, err := GetPciAddress("ib0", 33)
			Expect(result).To(Equal(""), "Not existing VF id should not return pci address")
			Expect(err).To(HaveOccurred(), "Not existing VF id should return an error")
		})
	})
	Context("Checking GetVFLinkNames function", func() {
		It("Assuming existing vf", func() {
			result, err := GetVFLinkNamesFromVFID("ib0", 0)
			Expect(result).To(ContainElement("ib1"), "Existing PF should have at least one VF")
			Expect(err).NotTo(HaveOccurred(), "Existing PF should not return an error")
		})
		It("Assuming not existing vf", func() {
			_, err := GetVFLinkNamesFromVFID("ib0", 3)
			Expect(err).To(HaveOccurred(), "Not existing VF should return an error")
		})
	})
	Context("Checking GetVFLinkIndex function", func() {
		It("Assuming existing vf", func() {
			ifIndex, err := GetVFLinkIndex("0000:af:06.0", defaults)
			Expect(err).NotTo(HaveOccurred())
			Expect(ifIndex).To(Equal(1001))
		})
		It("Assuming not existing vf", func() {
			_, err := GetVFLinkIndex("0000:af:07.0", shortLookup)
			Expect(err).To(HaveOccurred())
		})
		It("Netdev never appears (net dir stays empty)", func() {
			vfDir := filepath.Join(SysBusPci, "0000:af:06.3", "net")
			Expect(os.MkdirAll(vfDir, 0o755)).To(Succeed())
			defer os.RemoveAll(filepath.Join(SysBusPci, "0000:af:06.3"))
			_, err := GetVFLinkIndex("0000:af:06.3", shortLookup)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("has no entries"))
		})
		It("Netdev registered after the rebind (net dir empty, then populated)", func() {
			vfDir := filepath.Join(SysBusPci, "0000:af:06.4", "net")
			Expect(os.MkdirAll(vfDir, 0o755)).To(Succeed())
			defer os.RemoveAll(filepath.Join(SysBusPci, "0000:af:06.4"))
			go func() {
				time.Sleep(120 * time.Millisecond)
				_ = os.MkdirAll(filepath.Join(vfDir, "ib0"), 0o755)
				_ = os.WriteFile(filepath.Join(vfDir, "ib0", "ifindex"), []byte("1006\n"), 0o644)
			}()
			ifIndex, err := GetVFLinkIndex("0000:af:06.4", defaults)
			Expect(err).NotTo(HaveOccurred())
			Expect(ifIndex).To(Equal(1006))
		})
		It("Netdev renamed while being looked up", func() {
			// The net/ entry is the current name. Simulate udev renaming the interface after the
			// directory was listed: the old entry disappears, the ifindex file appears under the
			// new name, and the lookup must succeed by retrying.
			vfDir := filepath.Join(SysBusPci, "0000:af:06.2", "net")
			Expect(os.MkdirAll(filepath.Join(vfDir, "ib0"), 0o755)).To(Succeed())
			defer os.RemoveAll(filepath.Join(SysBusPci, "0000:af:06.2"))
			go func() {
				time.Sleep(120 * time.Millisecond)
				_ = os.Rename(filepath.Join(vfDir, "ib0"), filepath.Join(vfDir, "ibs5v0"))
				_ = os.WriteFile(filepath.Join(vfDir, "ibs5v0", "ifindex"), []byte("1005\n"), 0o644)
			}()
			ifIndex, err := GetVFLinkIndex("0000:af:06.2", defaults)
			Expect(err).NotTo(HaveOccurred())
			Expect(ifIndex).To(Equal(1005))
		})
	})
	Context("Checking WaitForUdevSettled function", func() {
		It("Device already processed by udev", func() {
			Expect(WaitForUdevSettled(1001, defaults)).To(Succeed())
		})
		It("udev finishes while we wait", func() {
			rec := filepath.Join(UdevDataDir, "n1007")
			defer os.Remove(rec)
			go func() {
				time.Sleep(60 * time.Millisecond)
				_ = os.WriteFile(rec, []byte("I:1789340000000001\nE:ID_NET_NAME=ibs5v0\n"), 0o644)
			}()
			start := time.Now()
			Expect(WaitForUdevSettled(1007, defaults)).To(Succeed())
			Expect(time.Since(start)).To(BeNumerically(">=", 50*time.Millisecond))
		})
		It("udev database not readable: the error propagates, it is not treated as no udev", func() {
			// A path below a regular file gives ENOTDIR, not ENOENT.
			dir := UdevDataDir
			blocker := filepath.Join(filepath.Dir(dir), "udev-blocker")
			Expect(os.WriteFile(blocker, []byte("x"), 0o600)).To(Succeed())
			defer os.Remove(blocker)
			UdevDataDir = filepath.Join(blocker, "data")
			defer func() { UdevDataDir = dir }()
			err := WaitForUdevSettled(1001, defaults)
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(ErrNoUdev))
			Expect(err).NotTo(MatchError(ErrUdevTimeout))
		})
		It("Unreadable record: the error propagates instead of waiting for the timeout", func() {
			rec := filepath.Join(UdevDataDir, "n1011")
			Expect(os.Mkdir(rec, 0o755)).To(Succeed()) // a directory where a file is expected: EISDIR
			defer os.RemoveAll(rec)
			err := WaitForUdevSettled(1011, shortUdev)
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(ErrUdevTimeout))
			Expect(err.Error()).To(ContainSubstring("failed to read udev record"))
		})
		It("Record shapes are pinned: a verbatim systemd 255 record settles, the same record mid-rename does not, and nothing else is read", func() {
			// Captured verbatim from /run/udev/data/n1177 on a Tetris H200 node (Ubuntu 24.04, systemd 255,
			// udev naming scheme v255) for the mlx5 IB VF 0000:76:00.1 after udev had renamed it to
			// ibs3v0. This is the systemd-internal database format the wait depends on (see the
			// udevNetdevSettled comment). The parser reads exactly two facts from it: an "I:" line
			// exists, and the exact line "E:ID_RENAMING=1" does or does not exist. Every other line must
			// be ignored, so a future systemd adding or reordering fields cannot change the answer.
			const settledRecord = "I:604505437564\n" +
				"E:NM_UNMANAGED=1\n" +
				"E:ID_NET_DRIVER=mlx5_core[ib_ipoib]\n" +
				"E:ID_BUS=pci\n" +
				"E:ID_VENDOR_ID=0x15b3\n" +
				"E:ID_MODEL_ID=0x101e\n" +
				"E:ID_PCI_CLASS_FROM_DATABASE=Network controller\n" +
				"E:ID_PCI_SUBCLASS_FROM_DATABASE=Infiniband controller\n" +
				"E:ID_VENDOR_FROM_DATABASE=Mellanox Technologies\n" +
				"E:ID_MODEL_FROM_DATABASE=ConnectX Family mlx5Gen Virtual Function\n" +
				"E:ID_NET_NAMING_SCHEME=v255\n" +
				"E:ID_NET_NAME_PATH=ibp118s0v0\n" +
				"E:ID_NET_NAME_SLOT=ibs3v0\n" +
				"E:ID_MM_CANDIDATE=1\n" +
				"E:ID_PATH=pci-0000:76:00.1\n" +
				"E:ID_PATH_TAG=pci-0000_76_00_1\n" +
				"E:ID_NET_LINK_FILE=/usr/lib/systemd/network/99-default.link\n" +
				"E:ID_NET_NAME=ibs3v0\n" +
				"E:SYSTEMD_ALIAS=/sys/subsystem/net/devices/ibs3v0\n" +
				"G:systemd\n" +
				"Q:systemd\n" +
				"V:1\n"
			// The same device while udev is still renaming it: udevd writes the record with the
			// ID_RENAMING property before the rtnetlink rename and rewrites it without the property
			// after processing the kernel's move uevent (observed on the same node with a 100 ms
			// sampler: "I:... E:ID_NET_NAME=ibs3v0 E:ID_RENAMING=1", then the same without the flag).
			renamingRecord := strings.Replace(settledRecord, "E:ID_NET_NAME=ibs3v0\n", "E:ID_NET_NAME=ibs3v0\nE:ID_RENAMING=1\n", 1)
			Expect(renamingRecord).NotTo(Equal(settledRecord))

			rec := filepath.Join(UdevDataDir, "n1177")
			defer os.Remove(rec)
			for _, tc := range []struct {
				name    string
				body    string
				settled bool
			}{
				{"verbatim settled record", settledRecord, true},
				{"verbatim record mid-rename", renamingRecord, false},
				{"only the initialized line", "I:1\n", true},
				{"initialized line last, CRLF-free, no trailing newline", "V:1\nE:ID_NET_NAME=ibs3v0\nI:604505437564", true},
				{"properties but no initialized line", "E:ID_NET_NAME=ibs3v0\nE:ID_RENAMING=1\nV:1\n", false},
				{"empty record", "", false},
				{"ID_RENAMING with any other value is not the pre-rename marker", "I:1\nE:ID_RENAMING=0\n", true},
				{"ID_RENAMING mentioned in another key is ignored", "I:1\nE:ID_RENAMING_OLD=1\nE:X=E:ID_RENAMING=1\n", true},
				{"unknown record types are ignored", "Z:future\nI:1\nW:whatever\n", true},
			} {
				Expect(os.WriteFile(rec, []byte(tc.body), 0o644)).To(Succeed(), tc.name)
				settled, err := udevNetdevSettled(rec)
				Expect(err).NotTo(HaveOccurred(), tc.name)
				Expect(settled).To(Equal(tc.settled), tc.name)
			}
		})
		It("Record written before the rename (ID_RENAMING=1) is not settled until udev rewrites it", func() {
			// systemd-udevd writes the record with ID_RENAMING=1 *before* renaming the interface and
			// drops the property once the kernel's move uevent has been processed.
			rec := filepath.Join(UdevDataDir, "n1009")
			defer os.Remove(rec)
			Expect(os.WriteFile(rec, []byte("E:ID_RENAMING=1\nV:1\nI:1789340000000002\nE:ID_NET_NAME=ibs5v0\n"), 0o644)).To(Succeed())
			settled, err := udevNetdevSettled(rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(settled).To(BeFalse())
			go func() {
				time.Sleep(60 * time.Millisecond)
				_ = os.WriteFile(rec, []byte("I:1789340000000002\nE:ID_NET_NAME=ibs5v0\n"), 0o644)
			}()
			start := time.Now()
			Expect(WaitForUdevSettled(1009, defaults)).To(Succeed())
			Expect(time.Since(start)).To(BeNumerically(">=", 50*time.Millisecond))
		})
		It("Record without an initialized line is not settled", func() {
			rec := filepath.Join(UdevDataDir, "n1010")
			defer os.Remove(rec)
			Expect(os.WriteFile(rec, []byte("E:ID_NET_NAME=ibs5v0\n"), 0o644)).To(Succeed())
			Expect(WaitForUdevSettled(1010, shortUdev)).To(MatchError(ErrUdevTimeout))
		})
		It("A poll interval longer than the timeout does not extend the wait", func() {
			start := time.Now()
			err := WaitForUdevSettled(1008, types.NetdevWait{UdevTimeout: 100 * time.Millisecond, UdevPoll: 5 * time.Second})
			Expect(err).To(MatchError(ErrUdevTimeout))
			Expect(time.Since(start)).To(BeNumerically(">=", 100*time.Millisecond))
			Expect(time.Since(start)).To(BeNumerically("<", 400*time.Millisecond))
		})
		It("udev never finishes: gives up after the timeout", func() {
			start := time.Now()
			Expect(WaitForUdevSettled(1008, shortUdev)).To(MatchError(ErrUdevTimeout))
			Expect(time.Since(start)).To(BeNumerically(">=", 150*time.Millisecond))
		})
		It("No udev on the host: returns at once", func() {
			dir := UdevDataDir
			UdevDataDir = filepath.Join(dir, "does-not-exist")
			defer func() { UdevDataDir = dir }()
			start := time.Now()
			Expect(WaitForUdevSettled(1001, defaults)).To(MatchError(ErrNoUdev))
			Expect(time.Since(start)).To(BeNumerically("<", 50*time.Millisecond))
		})
	})
	Context("Checking GetGUIDFromHwAddr function", func() {
		It("Valid IPoIB hardware address", func() {
			hwAddr, _ := net.ParseMAC("00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01")
			guid := GetGUIDFromHwAddr(hwAddr)
			Expect(guid).To(Equal("02:00:5e:10:00:00:00:01"))
		})
		It("Not valid IPoIB hardware address", func() {
			hwAddr, _ := net.ParseMAC("00:00:00:00:fe:80:00:00")
			guid := GetGUIDFromHwAddr(hwAddr)
			Expect(guid).To(Equal(""))
		})
	})
	Context("Checking IsVirtualFunction function", func() {
		It("Assuming VF device (has physfn)", func() {
			// This test assumes 0000:af:06.0 is a VF with physfn symlink
			result, err := IsVirtualFunction("0000:af:06.0")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for valid PCI address")
			Expect(result).To(Equal(true), "VF device should return true")
		})
		It("Assuming PF device (no physfn)", func() {
			// Test with the actual PF device 0000:af:00.1 (ib0) from fixture
			result, err := IsVirtualFunction("0000:af:00.1")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for valid PCI address")
			Expect(result).To(Equal(false), "PF device should return false")
		})
		It("Assuming VFIO VF device (has physfn)", func() {
			// Test with VFIO VF 0000:af:06.1 - should still be detected as VF
			result, err := IsVirtualFunction("0000:af:06.1")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for valid PCI address")
			Expect(result).To(Equal(true), "VFIO VF device should still return true")
		})
		It("Assuming non-existing device", func() {
			// This should return false and no error for non-existing device
			result, err := IsVirtualFunction("0000:ff:ff.f")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for non-existing device")
			Expect(result).To(Equal(false), "Non-existing device should return false")
		})
	})
	Context("Checking IsVfioPciDevice function", func() {
		It("Assuming device bound to vfio-pci driver", func() {
			// Test with VF (0000:af:06.1) that is bound to vfio-pci in the mock
			result, err := IsVfioPciDevice("0000:af:06.1")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for valid PCI address")
			Expect(result).To(Equal(true), "Device bound to vfio-pci driver should return true")
		})
		It("Assuming PF device bound to mlx5_core driver", func() {
			// Test with PF (0000:af:00.1) that should be bound to mlx5_core
			result, err := IsVfioPciDevice("0000:af:00.1")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for valid PCI address")
			Expect(result).To(Equal(false), "PF bound to non-vfio driver should return false")
		})
		It("Assuming VF device bound to mlx5_core driver", func() {
			// Test with VF (0000:af:06.0) that should be bound to mlx5_core
			result, err := IsVfioPciDevice("0000:af:06.0")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for valid PCI address")
			Expect(result).To(Equal(false), "VF bound to non-vfio driver should return false")
		})
		It("Assuming device not bound to any driver", func() {
			// Non-existing device path will fail readlink, returning false
			result, err := IsVfioPciDevice("0000:ff:ff.f")
			Expect(err).NotTo(HaveOccurred(), "Should not return error for unbound device")
			Expect(result).To(Equal(false), "Device not bound to driver should return false")
		})
	})
})
