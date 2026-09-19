package types

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"

	rdmatypes "github.com/k8snetworkplumbingwg/rdma-cni/pkg/types"
)

// NetConf extends types.PluginConf for ib-sriov-cni
type NetConf struct {
	types.PluginConf
	IbSriovNetConf
}

type IbSriovNetConf struct {
	Master              string
	DeviceID            string `json:"deviceID"` // PCI address of a VF in valid sysfs format
	VFID                int
	HostIFNames         string // VF netdevice name(s)
	HostIFGUID          string // VF netdevice GUID
	ContIFNames         string // VF names after in the container; used during deletion
	GUID                string `json:"-"` // Taken from either CNI_ARGS "guid" attribute or from RuntimeConfig
	PKey                string `json:"pkey"`
	LinkState           string `json:"link_state,omitempty"` // auto|enable|disable
	RdmaIsolation       bool   `json:"rdmaIsolation,omitempty"`
	IBKubernetesEnabled bool   `json:"ibKubernetesEnabled,omitempty"`
	VfioPciMode         bool   `json:"vfioPciMode,omitempty"` // Skip SR-IOV network setup, default false
	// Timing for finding the VF netdev after a rebind and waiting for udev to finish with it.
	// All optional; Go duration strings ("50ms", "3s"). See NetdevWait for the defaults.
	VFNetdevLookupAttempts int    `json:"vfNetdevLookupAttempts,omitempty"` // total attempts; 0 or omitted uses default 20
	VFNetdevLookupInterval string `json:"vfNetdevLookupInterval,omitempty"` // between attempts, default "50ms"
	UdevSettleTimeout      string `json:"udevSettleTimeout,omitempty"`      // default "3s", minimum "1s"
	UdevSettlePoll         string `json:"udevSettlePoll,omitempty"`         // default "25ms"
	IsVFDevice             bool   `json:"-"`                                // Runtime flag: true if device is VF, false if PF
	RdmaNetState           rdmatypes.RdmaNetState
	RuntimeConfig          RuntimeConf `json:"runtimeConfig,omitempty"`
	Args                   struct {
		CNI map[string]string `json:"cni"`
	} `json:"args"`
}

// NetdevWait is the timing for finding a VF netdev after a rebind (the netdev may not be registered
// yet, or its sysfs entry may be mid-rename) and for waiting until udev has finished with it.
type NetdevWait struct {
	LookupAttempts int
	LookupInterval time.Duration
	UdevTimeout    time.Duration
	UdevPoll       time.Duration
}

// MinUdevSettleTimeout is the smallest udevSettleTimeout accepted. udev needs 0.1 to 1.5 s to finish
// with a freshly rebound VF netdev on the hardware measured so far; a shorter budget would fail ADDs
// that udev was about to complete. There is deliberately no way to disable the wait: skipping it is
// exactly the rename race this plugin version removes, and a host without udev is handled on its own
// (ErrNoUdev) without configuration.
const MinUdevSettleTimeout = time.Second

// DefaultNetdevWait is used for every field left unset in the network configuration.
var DefaultNetdevWait = NetdevWait{
	LookupAttempts: 20,
	LookupInterval: 50 * time.Millisecond,
	UdevTimeout:    3 * time.Second,
	UdevPoll:       25 * time.Millisecond,
}

// NetdevWait parses the optional timing fields of the network configuration, filling defaults for
// the ones not set. Zero lookup attempts also means the default. udevSettleTimeout must be at least
// MinUdevSettleTimeout.
func (n *NetConf) NetdevWait() (NetdevWait, error) {
	w := DefaultNetdevWait
	if n.VFNetdevLookupAttempts < 0 {
		return w, fmt.Errorf("vfNetdevLookupAttempts must not be negative: %d", n.VFNetdevLookupAttempts)
	}
	if n.VFNetdevLookupAttempts > 0 {
		w.LookupAttempts = n.VFNetdevLookupAttempts
	}
	var err error
	if n.VFNetdevLookupInterval != "" {
		if w.LookupInterval, err = parsePositiveDuration("vfNetdevLookupInterval", n.VFNetdevLookupInterval); err != nil {
			return w, err
		}
	}
	if n.UdevSettlePoll != "" {
		if w.UdevPoll, err = parsePositiveDuration("udevSettlePoll", n.UdevSettlePoll); err != nil {
			return w, err
		}
	}
	if n.UdevSettleTimeout != "" {
		d, err := time.ParseDuration(n.UdevSettleTimeout)
		if err != nil || d < MinUdevSettleTimeout {
			return w, fmt.Errorf("invalid udevSettleTimeout %q: want a duration of at least %s, such as \"3s\"; the udev wait cannot be disabled",
				n.UdevSettleTimeout, MinUdevSettleTimeout)
		}
		w.UdevTimeout = d
	}
	return w, nil
}

func parsePositiveDuration(field, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: want a positive duration such as \"50ms\"", field, value)
	}
	return d, nil
}

func (n NetConf) MarshalJSON() ([]byte, error) {
	pluginConfBytes, err := json.Marshal(&n.PluginConf)
	if err != nil {
		return nil, fmt.Errorf("error serializing delegate netConf: %v", err)
	}

	ibSriovNetConfBytes, err := json.Marshal(&n.IbSriovNetConf)
	if err != nil {
		return nil, fmt.Errorf("error serializing delegate sriovNetConf: %v", err)
	}

	netConfMap := make(map[string]interface{})
	if err := json.Unmarshal(pluginConfBytes, &netConfMap); err != nil {
		return nil, err
	}

	ibSriovNetConfMap := make(map[string]interface{})
	if err := json.Unmarshal(ibSriovNetConfBytes, &ibSriovNetConfMap); err != nil {
		return nil, err
	}

	for k, v := range netConfMap {
		ibSriovNetConfMap[k] = v
	}

	ibSriovNetConfBytes, err = json.Marshal(ibSriovNetConfMap)
	if err != nil {
		return nil, err
	}

	return ibSriovNetConfBytes, nil
}

// RuntimeConf represents the plugin's runtime configurations
type RuntimeConf struct {
	InfinibandGUID string `json:"infinibandGUID"`
}

// Manager provides interface invoke sriov nic related operations
type Manager interface {
	SetupVF(conf *NetConf, podifName string, cid string, netns ns.NetNS) error
	ReleaseVF(conf *NetConf, podifName string, cid string, netns ns.NetNS) error
	ResetVFConfig(conf *NetConf) error
	ApplyVFConfig(conf *NetConf) error
}

// mocked netlink interface
// required for unit tests

// NetlinkManager is an interface to mock nelink library
type NetlinkManager interface {
	LinkByName(string) (netlink.Link, error)
	LinkByIndex(int) (netlink.Link, error)
	LinkSetUp(netlink.Link) error
	LinkSetDown(netlink.Link) error
	LinkSetNsFd(netlink.Link, int) error
	LinkSetName(netlink.Link, string) error
	LinkSetVfState(netlink.Link, int, uint32) error
	LinkSetVfPortGUID(netlink.Link, int, net.HardwareAddr) error
	LinkSetVfNodeGUID(netlink.Link, int, net.HardwareAddr) error
	LinkDelAltName(netlink.Link, string) error
}

// PciUtils is interface to help in SR-IOV functions
type PciUtils interface {
	GetSriovNumVfs(ifName string) (int, error)
	GetVFLinkNamesFromVFID(pfName string, vfID int) ([]string, error)
	GetPciAddress(ifName string, vf int) (string, error)
	RebindVf(pfName, vfPciAddress string) error
}
