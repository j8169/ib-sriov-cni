package sriov

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/k8snetworkplumbingwg/sriovnet"
	"github.com/vishvananda/netlink"

	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types"
	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/utils"
)

// MyNetlink NetlinkManager
type MyNetlink struct {
}

// LinkByName implements NetlinkManager
func (n *MyNetlink) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}

// LinkByIndex implements NetlinkManager
func (n *MyNetlink) LinkByIndex(index int) (netlink.Link, error) {
	return netlink.LinkByIndex(index)
}

// LinkSetUp using NetlinkManager
func (n *MyNetlink) LinkSetUp(link netlink.Link) error {
	return netlink.LinkSetUp(link)
}

// LinkSetDown using NetlinkManager
func (n *MyNetlink) LinkSetDown(link netlink.Link) error {
	return netlink.LinkSetDown(link)
}

// LinkSetNsFd using NetlinkManager
func (n *MyNetlink) LinkSetNsFd(link netlink.Link, fd int) error {
	return netlink.LinkSetNsFd(link, fd)
}

// LinkSetName using NetlinkManager
func (n *MyNetlink) LinkSetName(link netlink.Link, name string) error {
	return netlink.LinkSetName(link, name)
}

// LinkSetVfState using NetlinkManager
func (n *MyNetlink) LinkSetVfState(link netlink.Link, vf int, state uint32) error {
	return netlink.LinkSetVfState(link, vf, state)
}

// LinkSetVfPortGUID using NetlinkManager
func (n *MyNetlink) LinkSetVfPortGUID(link netlink.Link, vf int, portGUID net.HardwareAddr) error {
	return netlink.LinkSetVfPortGUID(link, vf, portGUID)
}

// LinkSetVfNodeGUID using NetlinkManager
func (n *MyNetlink) LinkSetVfNodeGUID(link netlink.Link, vf int, nodeGUID net.HardwareAddr) error {
	return netlink.LinkSetVfNodeGUID(link, vf, nodeGUID)
}

// LinkDelAltName using NetlinkManager
func (n *MyNetlink) LinkDelAltName(link netlink.Link, altName string) error {
	return netlink.LinkDelAltName(link, altName)
}

type pciUtilsImpl struct{}

func (p *pciUtilsImpl) GetSriovNumVfs(ifName string) (int, error) {
	return utils.GetSriovNumVfs(ifName)
}

func (p *pciUtilsImpl) GetVFLinkNamesFromVFID(pfName string, vfID int) ([]string, error) {
	return utils.GetVFLinkNamesFromVFID(pfName, vfID)
}

func (p *pciUtilsImpl) GetPciAddress(ifName string, vf int) (string, error) {
	return utils.GetPciAddress(ifName, vf)
}

// RebindVf unbind then bind the vf
func (p *pciUtilsImpl) RebindVf(pfName, vfPciAddress string) error {
	pfHandle, err := sriovnet.GetPfNetdevHandle(pfName)
	if err != nil {
		return err
	}
	var vf *sriovnet.VfObj
	found := false
	for _, vfObj := range pfHandle.List {
		if vfObj.PciAddress == vfPciAddress {
			vf = vfObj
			found = true
		}
	}
	if !found {
		return fmt.Errorf("failed to find VF %s for PF %s", vfPciAddress, pfName)
	}

	err = sriovnet.UnbindVf(pfHandle, vf)
	if err != nil {
		return err
	}

	err = sriovnet.BindVf(pfHandle, vf)
	if err != nil {
		return err
	}
	return nil
}

type sriovManager struct {
	nLink types.NetlinkManager
	utils types.PciUtils
}

// NewSriovManager returns an instance of SriovManager
func NewSriovManager() types.Manager {
	return &sriovManager{
		nLink: &MyNetlink{},
		utils: &pciUtilsImpl{},
	}
}

// dropAltNames removes every alt name of link that equals one of names. The kernel refuses to rename
// a device to one of its own alt names (EEXIST), and alt names survive a netns move, so a stale one
// would break the rename to the pod name on ADD or back to the host name on DEL. With the udev wait in
// place udev's own rename succeeds and it never records the persistent name as an alt name, so this
// is reachable only when the wait is disabled in the configuration, times out on the DEL path (where
// the CNI proceeds), or something outside the CNI added the name. It stays because the DEL rename it
// protects is the one that would otherwise strand the VF on the host still carrying the pod GUID.
func (s *sriovManager) dropAltNames(link netlink.Link, names ...string) error {
	for _, altName := range link.Attrs().AltNames {
		for _, name := range names {
			if altName != name {
				continue
			}
			if err := s.nLink.LinkDelAltName(link, altName); err != nil {
				return fmt.Errorf("failed to remove alt name %s from %s: %v", altName, link.Attrs().Name, err)
			}
			break
		}
	}
	return nil
}

// SetupVF sets up a VF in Pod netns
func (s *sriovManager) SetupVF(conf *types.NetConf, podifName, cid string, netns ns.NetNS) error {
	// This field tracks whether the current ADD moved the interface and what name it has there.
	// Do not let cached or input state make failed-ADD cleanup release an interface we did not move.
	conf.ContIFNames = ""
	// ApplyVFConfig rebound the VF, so its netdev was just re-created and udev is renaming it
	// (ib0 -> persistent name) and adding alt names concurrently with us. Find it by ifindex, which
	// a rename does not change, then wait for udev to finish with it so that the name and alt names
	// read below are final rather than whatever udev had got to.
	wait, err := conf.NetdevWait()
	if err != nil {
		return err
	}
	ifIndex, err := utils.GetVFLinkIndex(conf.DeviceID, wait)
	if err != nil {
		return fmt.Errorf("failed to get VF %s netdev after rebind: %v", conf.DeviceID, err)
	}
	// A timeout fails the ADD rather than racing udev; the runtime retries, and by then udev is done.
	if err := utils.WaitForUdevSettled(ifIndex, wait); err != nil && !errors.Is(err, utils.ErrNoUdev) {
		return fmt.Errorf("VF %s netdev after rebind: %v", conf.DeviceID, err)
	}

	linkObj, err := s.nLink.LinkByIndex(ifIndex)
	if err != nil {
		return fmt.Errorf("error getting VF %s netdevice with ifindex %d: %v", conf.DeviceID, ifIndex, err)
	}
	linkName := linkObj.Attrs().Name

	// tempName used as intermediary name to avoid name conflicts
	tempName := fmt.Sprintf("vfdev%d", ifIndex)

	// 1. Set link down
	if err := s.nLink.LinkSetDown(linkObj); err != nil {
		return fmt.Errorf("failed to down vf device %q: %v", linkName, err)
	}

	// 2. Set temp name
	if err := s.nLink.LinkSetName(linkObj, tempName); err != nil {
		return fmt.Errorf("error setting temp IF name %s for %s", tempName, linkName)
	}

	// 3. Drop alt names that would block the renames to the pod name and back to the host name
	linkObj, err = s.nLink.LinkByIndex(ifIndex)
	if err != nil {
		return fmt.Errorf("error getting VF netdevice with ifindex %d: %v", ifIndex, err)
	}
	if err := s.dropAltNames(linkObj, linkName, conf.HostIFNames, podifName); err != nil {
		return err
	}

	// 4. Change netns
	if err := s.nLink.LinkSetNsFd(linkObj, int(netns.Fd())); err != nil {
		return fmt.Errorf("failed to move IF %s to netns: %q", tempName, err)
	}
	// From this point cleanup must look in the pod netns. Keep the actual current name so a failure
	// before the final rename can still move the VF back.
	conf.ContIFNames = tempName

	if err := netns.Do(func(_ ns.NetNS) error {
		// 5. Set Pod IF name
		if err := s.nLink.LinkSetName(linkObj, podifName); err != nil {
			return fmt.Errorf("error setting container interface name %s for %s", linkName, tempName)
		}
		conf.ContIFNames = podifName

		// 6. Bring IF up in Pod netns
		if err := s.nLink.LinkSetUp(linkObj); err != nil {
			return fmt.Errorf("error bringing interface up in container ns: %q", err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("error setting up interface in container namespace: %q", err)
	}
	return nil
}

// ReleaseVF reset a VF from Pod netns and return it to init netns
func (s *sriovManager) ReleaseVF(conf *types.NetConf, podifName, cid string, netns ns.NetNS) error {
	initns, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("failed to get init netns: %v", err)
	}

	if len(conf.ContIFNames) < 1 && len(conf.ContIFNames) != len(conf.HostIFNames) {
		return fmt.Errorf(
			"number of interface names mismatch ContIFNames: %d HostIFNames: %d",
			len(conf.ContIFNames), len(conf.HostIFNames))
	}

	return netns.Do(func(_ ns.NetNS) error {
		// get VF device
		linkObj, err := s.nLink.LinkByName(podifName)
		if err != nil {
			return fmt.Errorf("failed to get netlink device with name %s: %q", podifName, err)
		}

		// shutdown VF device
		if err = s.nLink.LinkSetDown(linkObj); err != nil {
			return fmt.Errorf("failed to set link %s down: %q", podifName, err)
		}

		// an alt name equal to the host name would make the rename below fail (see dropAltNames)
		if err := s.dropAltNames(linkObj, conf.HostIFNames); err != nil {
			return err
		}

		// rename VF device
		err = s.nLink.LinkSetName(linkObj, conf.HostIFNames)
		if err != nil {
			return fmt.Errorf("failed to rename link %s to host name %s: %q", podifName, conf.HostIFNames, err)
		}

		// move VF device to init netns
		if err = s.nLink.LinkSetNsFd(linkObj, int(initns.Fd())); err != nil {
			return fmt.Errorf("failed to move interface %s to init netns: %v", conf.HostIFNames, err)
		}

		return nil
	})
}

// applyVFGuid handles VF GUID configuration and validation for both VFIO and regular VFs
func (s *sriovManager) applyVFGuid(conf *types.NetConf, pfLink netlink.Link) error {
	if conf.GUID != "" {
		if !utils.IsValidGUID(conf.GUID) {
			return fmt.Errorf("invalid guid %s", conf.GUID)
		}

		// For VFIO VF devices, we can't read current GUID from VF interface
		if conf.VfioPciMode {
			// Save all-F GUID to reset to during deletion
			conf.HostIFGUID = utils.DefaultGUID
		} else {
			// Regular VF: save current GUID from VF link
			vfLink, err := s.nLink.LinkByName(conf.HostIFNames)
			if err != nil {
				return fmt.Errorf("failed to lookup vf %q: %v", conf.HostIFNames, err)
			}
			conf.HostIFGUID = vfLink.Attrs().HardwareAddr.String()[36:]
			// The VF already carrying this pod's GUID means an earlier ADD wrote it and failed before it
			// could reset the VF. Do not save it as the host GUID, or DEL would write a pod GUID back onto
			// the idle VF; treat the VF as having no host GUID so DEL resets it to the default.
			if strings.EqualFold(conf.HostIFGUID, conf.GUID) {
				conf.HostIFGUID = utils.DefaultGUID
			}
		}

		// Set link guid
		// If the port-GUID write fails after the node-GUID write, put the node GUID back so the VF is
		// not left with a split identity; the deferred reset in handleVFAdd then rebinds it whole.
		if err := s.setVfGUID(conf, pfLink, conf.GUID, conf.HostIFGUID); err != nil {
			return err
		}
	} else if !conf.VfioPciMode {
		// Verify VF have valid GUID (skip for VFIO as we can't access VF interface)
		vfLink, err := s.nLink.LinkByName(conf.HostIFNames)
		if err != nil {
			return fmt.Errorf("failed to lookup vf %q: %v", conf.HostIFNames, err)
		}

		guid := utils.GetGUIDFromHwAddr(vfLink.Attrs().HardwareAddr)
		if guid == "" || utils.IsAllZeroGUID(guid) || utils.IsAllOnesGUID(guid) {
			return fmt.Errorf("VF %s GUID is not valid", conf.HostIFNames)
		}
	}
	return nil
}

// ApplyVFConfig configure a VF with parameters given in NetConf
func (s *sriovManager) ApplyVFConfig(conf *types.NetConf) error {
	pfLink, err := s.nLink.LinkByName(conf.Master)
	if err != nil {
		return fmt.Errorf("failed to lookup master %q: %v", conf.Master, err)
	}

	// Set link state
	if conf.LinkState != "" {
		var state uint32
		switch conf.LinkState {
		case "auto":
			state = netlink.VF_LINK_STATE_AUTO
		case "enable":
			state = netlink.VF_LINK_STATE_ENABLE
		case "disable":
			state = netlink.VF_LINK_STATE_DISABLE
		default:
			// the value should have been validated earlier, return error if we somehow got here
			return fmt.Errorf("unknown link state %s when setting it for vf %d: %v", conf.LinkState, conf.VFID, err)
		}
		if err = s.nLink.LinkSetVfState(pfLink, conf.VFID, state); err != nil {
			return fmt.Errorf("failed to set vf %d link state to %d: %v", conf.VFID, state, err)
		}
	}

	// Handle VF GUID configuration
	return s.applyVFGuid(conf, pfLink)
}

// restoreVFName restores VF name from conf
func (s *sriovManager) restoreVFName(conf *types.NetConf) error {
	// Same as in SetupVF: the VF was just rebound, so find it by ifindex and let udev finish first.
	wait, err := conf.NetdevWait()
	if err != nil {
		return fmt.Errorf("restoreVFName error: %v", err)
	}
	ifIndex, err := utils.GetVFLinkIndex(conf.DeviceID, wait)
	if err != nil {
		return fmt.Errorf("restoreVFName error: failed to get netdev for VF %s, %v", conf.DeviceID, err)
	}
	// On the DEL / reset path a slow udev must not fail the teardown; on timeout proceed as before.
	_ = utils.WaitForUdevSettled(ifIndex, wait)

	linkObj, err := s.nLink.LinkByIndex(ifIndex)
	if err != nil {
		return fmt.Errorf("restoreVFName error: failed to get link with ifindex %d for VF %s, %v",
			ifIndex, conf.DeviceID, err)
	}

	linkName := linkObj.Attrs().Name
	if linkName == conf.HostIFNames {
		// VF has expected name, no need to set it
		return nil
	}

	if err := s.dropAltNames(linkObj, conf.HostIFNames); err != nil {
		return fmt.Errorf("restoreVFName error: %v", err)
	}

	err = s.nLink.LinkSetName(linkObj, conf.HostIFNames)
	if err != nil {
		return fmt.Errorf("restoreVFName error: failed to rename link %s to host name %s, %v",
			linkName, conf.HostIFNames, err)
	}
	return nil
}

// ResetVFConfig reset a VF with default values
func (s *sriovManager) ResetVFConfig(conf *types.NetConf) error {
	pfLink, err := s.nLink.LinkByName(conf.Master)
	if err != nil {
		return fmt.Errorf("failed to lookup master %q: %v", conf.Master, err)
	}
	var errs []error

	// Reset link state to `auto`
	if conf.LinkState != "" {
		// While resetting to `auto` can be a reasonable thing to do regardless of whether it was explicitly
		// specified in the network definition, reset only when link_state was explicitly specified, to
		// accommodate for drivers / NICs that don't support the netlink command (e.g. igb driver)
		if err = s.nLink.LinkSetVfState(pfLink, conf.VFID, 0); err != nil {
			errs = append(errs, fmt.Errorf("failed to set link state to auto for vf %d: %v", conf.VFID, err))
		}
	}

	// Reset link guid
	// if the host guid is all zeros which is invalid guid replace it with all F guid
	// This happen when create a VF it guid is all zeros
	if conf.HostIFGUID != "" {
		if utils.IsAllZeroGUID(conf.HostIFGUID) {
			conf.HostIFGUID = utils.DefaultGUID
		}

		// No rollback target on the reset path: the pod GUID is not in the cached NetConf on DEL, and
		// putting it back would be the wrong direction anyway.
		if err := s.setVfGUID(conf, pfLink, conf.HostIFGUID, ""); err != nil {
			errs = append(errs, err)
		} else if !conf.VfioPciMode {
			// setVfGUID causes the VF to rebind, which changes its name. Restore it only after
			// the GUID reset and rebind succeeded.
			if err := s.restoreVFName(conf); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// setVfGUID writes guidAddr as the VF's node and port GUID and rebinds the VF to apply it. If the
// port-GUID write fails after the node GUID was written and rollbackTarget is set, the node GUID is
// written back to rollbackTarget so the VF is not left with a split identity. rollbackTarget is
// validated before anything is written.
func (s *sriovManager) setVfGUID(conf *types.NetConf, pfLink netlink.Link, guidAddr, rollbackTarget string) error {
	guid, err := net.ParseMAC(guidAddr)
	if err != nil {
		return fmt.Errorf("failed to parse guid %s: %v", guidAddr, err)
	}
	var rollbackGUID net.HardwareAddr
	if rollbackTarget != "" {
		if rollbackGUID, err = net.ParseMAC(rollbackTarget); err != nil {
			return fmt.Errorf("failed to parse rollback guid %s: %w", rollbackTarget, err)
		}
	}
	err = s.nLink.LinkSetVfNodeGUID(pfLink, conf.VFID, guid)
	if err != nil {
		return fmt.Errorf("failed to add node guid %s: %v", guid, err)
	}
	err = s.nLink.LinkSetVfPortGUID(pfLink, conf.VFID, guid)
	if err != nil {
		portErr := fmt.Errorf("failed to add port guid %s: %v", guid, err)
		if rollbackGUID == nil {
			return portErr
		}
		if rollbackErr := s.nLink.LinkSetVfNodeGUID(pfLink, conf.VFID, rollbackGUID); rollbackErr != nil {
			return errors.Join(portErr, fmt.Errorf("failed to roll back node guid to %s: %w", rollbackTarget, rollbackErr))
		}
		return portErr
	}
	// For VFIO devices, skip rebind as the device is bound to vfio-pci driver
	// and doesn't have a network interface that can be unbound/rebound
	if !conf.VfioPciMode {
		// unbind vf then bind it to apply the guid
		err = s.utils.RebindVf(conf.Master, conf.DeviceID)
		if err != nil {
			return err
		}
	}
	return nil
}
