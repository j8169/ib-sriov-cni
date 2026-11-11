package sriov

import (
	"errors"
	"net"
	"os"
	"path/filepath"

	"github.com/containernetworking/plugins/pkg/ns"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"github.com/vishvananda/netlink"

	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types"
	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types/mocks"
	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/utils"
)

// FakeLink is a dummy netlink struct used during testing
type FakeLink struct {
	netlink.LinkAttrs
}

func (l *FakeLink) Attrs() *netlink.LinkAttrs {
	return &l.LinkAttrs
}

func (l *FakeLink) Type() string {
	return "FakeLink"
}

// Fake NS - implements ns.NetNS interface
type fakeNetNS struct {
	closed bool
	fd     uintptr
	path   string
}

func (f *fakeNetNS) Do(toRun func(ns.NetNS) error) error {
	return toRun(f)
}

func (f *fakeNetNS) Set() error {
	return nil
}

func (f *fakeNetNS) Path() string {
	return f.path
}

func (f *fakeNetNS) Fd() uintptr {
	return f.fd
}

func (f *fakeNetNS) Close() error {
	f.closed = true
	return nil
}

func newFakeNs() ns.NetNS {
	return &fakeNetNS{
		closed: false,
		fd:     17,
		path:   "/proc/4123/ns/net",
	}
}

var _ = Describe("Sriov", func() {

	Context("Checking ApplyVFConfig function", func() {
		var (
			netconf *types.NetConf
		)

		BeforeEach(func() {
			netconf = &types.NetConf{
				IbSriovNetConf: types.IbSriovNetConf{
					Master:      "ibFake0",
					DeviceID:    "0000:af:06.0",
					VFID:        0,
					HostIFNames: "ibFake5",
				},
			}
		})

		It("ApplyVFConfig with valid GUID", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}
			hostGUID := "11:22:33:00:00:aa:bb:cc"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}
			netconf.GUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)

			mockedPciUtils.On("RebindVf", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
			Expect(netconf.HostIFGUID).To(Equal(hostGUID))
		})
		It("ApplyVFConfig without GUID, VF's GUID all zeroes", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			hostGUID := "00:00:00:00:00:00:00:00"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}
			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
		It("ApplyVFConfig without GUID, VF's GUID all 'F's", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			hostGUID := "ff:ff:ff:ff:ff:ff:ff:ff"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}
			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
		It("ApplyVFConfig without GUID, VF's GUID is configured", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			hostGUID := "11:22:33:00:00:aa:bb:cc"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}
			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
			Expect(netconf.HostIFGUID).To(Equal(""))
			Expect(netconf.GUID).To(Equal(""))
		})
		It("ApplyVFConfig with invalid GUID - wrong characters", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.GUID = "invalid GUID"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger}
			err := sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
		It("ApplyVFConfig with invalid GUID - wrong length", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.GUID = "00:11:22:33:44:55:66"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger}
			err := sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
		It("ApplyVFConfig with invalid GUID - all zeros guid", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.GUID = "00:00:00:00:00:00:00:00"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger}
			err := sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
		It("ApplyVFConfig with invalid GUID - invalid guid address", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.GUID = "00:AF-3B-0123:21:3322"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger}
			err := sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
		It("ApplyVFConfig check guid - failed to get vf link", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.GUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkByName", netconf.Master).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkByName", netconf.HostIFNames).Return(nil, errors.New("mocked failed"))

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(`failed to lookup vf "ibFake5": mocked failed`))
		})
		It("ApplyVFConfig check guid - failed to set node guid", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}

			hostGUID := "11:22:33:00:00:aa:bb:cc"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}

			netconf.GUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkByName", mock.Anything).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.Anything, mock.Anything).Return(
				errors.New("mocked failed"))

			sm := sriovManager{nLink: mockedNetLinkManger}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(`failed to add node guid 01:23:45:67:89:ab:cd:ef: mocked failed`))
			Expect(netconf.HostIFGUID).To(Equal(hostGUID))
		})
		It("ApplyVFConfig check guid - failed to set port guid", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}

			hostGUID := "11:22:33:00:00:aa:bb:cc"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}

			netconf.GUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkByName", mock.Anything).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.Anything, mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.Anything, mock.Anything).Return(
				errors.New("mocked failed"))

			sm := sriovManager{nLink: mockedNetLinkManger}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(`failed to add port guid 01:23:45:67:89:ab:cd:ef: mocked failed`))
			Expect(netconf.HostIFGUID).To(Equal(hostGUID))
		})
		It("ApplyVFConfig check guid - failed to rebind after set guid", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			hostGUID := "11:22:33:00:00:aa:bb:cc"
			gid, err := net.ParseMAC("00:00:04:a5:fe:80:00:00:00:00:00:00:" + hostGUID)
			Expect(err).ToNot(HaveOccurred())

			fakeLink := &FakeLink{netlink.LinkAttrs{
				HardwareAddr: gid,
			}}

			netconf.GUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkByName", mock.Anything).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.Anything, mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.Anything, mock.Anything).Return(nil)

			mockedPciUtils.On("RebindVf", netconf.Master, netconf.DeviceID).Return(errors.New("mocked failed"))

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err = sm.ApplyVFConfig(netconf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("mocked failed"))
			Expect(netconf.HostIFGUID).To(Equal(hostGUID))
		})
		It("ApplyVFConfig with valid GUID and VfioPciMode VF (no network interface) - should return success after setting GUID", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.GUID = "01:23:45:67:89:ab:cd:ef"
			netconf.VfioPciMode = true
			netconf.HostIFNames = "" // VFIO VF has no network interface

			// Only PF link is needed for VFIO VF
			mockedNetLinkManger.On("LinkByName", netconf.Master).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)

			mockedPciUtils.On("RebindVf", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ApplyVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
			// For VFIO VF, HostIFGUID should be set to all-F for reset during deletion
			Expect(netconf.HostIFGUID).To(Equal("FF:FF:FF:FF:FF:FF:FF:FF"))
		})
	})
	Context("Checking SetupVF function", func() {
		var (
			podifName string
			contID    string
			netconf   *types.NetConf
		)

		BeforeEach(func() {
			podifName = "net1"
			contID = "dummycid"
			netconf = &types.NetConf{
				IbSriovNetConf: types.IbSriovNetConf{
					Master:      "ib0",
					DeviceID:    "0000:af:06.0",
					VFID:        0,
					HostIFNames: "ib1",
				},
			}
		})

		It("Assuming existing interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{
				Index: 1000,
				Name:  "dummylink",
			}}

			mocked.On("LinkByIndex", 1001).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(nil)
			mocked.On("LinkSetUp", fakeLink).Return(nil)
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).NotTo(HaveOccurred())
			Expect(netconf.ContIFNames).To(Equal(podifName))
		})
		It("Assuming non existing interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			mocked.On("LinkByIndex", 1001).Return(nil, errors.New("not fount"))
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming existing interface not able to set down", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{
				Index: 1000,
				Name:  "dummylink",
			}}

			mocked.On("LinkByIndex", 1001).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming failed to change name", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{
				Index: 1000,
				Name:  "dummylink",
			}}

			mocked.On("LinkByIndex", 1001).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming failed to move interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{
				Index: 1000,
				Name:  "dummylink",
			}}

			mocked.On("LinkByIndex", 1001).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.Anything).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Remove altName", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			// "ib1" is the host name the VF is renamed back to on release, "dummylink" is its current
			// name, and "net1" is the next rename target. All three must be dropped; "enp175s6" stays.
			fakeLink := &FakeLink{netlink.LinkAttrs{
				Index:    1001,
				Name:     "dummylink",
				AltNames: []string{"enp175s6", "ib1", "dummylink", podifName},
			}}

			mocked.On("LinkByIndex", 1001).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(nil)
			mocked.On("LinkDelAltName", fakeLink, "ib1").Return(nil)
			mocked.On("LinkDelAltName", fakeLink, "dummylink").Return(nil)
			mocked.On("LinkDelAltName", fakeLink, podifName).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(nil)
			mocked.On("LinkSetUp", fakeLink).Return(nil)
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).NotTo(HaveOccurred())
			mocked.AssertCalled(GinkgoT(), "LinkDelAltName", fakeLink, "ib1")
			mocked.AssertCalled(GinkgoT(), "LinkDelAltName", fakeLink, "dummylink")
			mocked.AssertCalled(GinkgoT(), "LinkDelAltName", fakeLink, podifName)
			mocked.AssertNotCalled(GinkgoT(), "LinkDelAltName", fakeLink, "enp175s6")
		})
		It("Does not manipulate the interface while udev is still renaming it", func() {
			record := filepath.Join(utils.UdevDataDir, "n1001")
			original, err := os.ReadFile(record)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(os.WriteFile(record, original, 0o644)).To(Succeed()) })
			Expect(os.WriteFile(record, []byte("I:123\nE:ID_RENAMING=1\n"), 0o644)).To(Succeed())
			netconf.UdevSettleTimeout = "1s"
			mocked := &mocks.NetlinkManager{}
			sm := sriovManager{nLink: mocked}

			err = sm.SetupVF(netconf, podifName, contID, newFakeNs())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("udev did not finish"))
			Expect(mocked.Calls).To(BeEmpty())
		})
		It("Never looks the VF up by name (udev rename race)", func() {
			// The netdev is found by ifindex from the VF's PCI sysfs entry, and the temp rename
			// uses that ifindex, so a udev rename at any point cannot make the lookup fail.
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{Index: 1001, Name: "ib0"}}

			mocked.On("LinkByIndex", 1001).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, "vfdev1001").Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(nil)
			mocked.On("LinkSetName", fakeLink, podifName).Return(nil)
			mocked.On("LinkSetUp", fakeLink).Return(nil)
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).NotTo(HaveOccurred())
			mocked.AssertNotCalled(GinkgoT(), "LinkByName", mock.Anything)
			mocked.AssertCalled(GinkgoT(), "LinkSetName", fakeLink, "vfdev1001")
		})
	})
	Context("Checking ReleaseVF function", func() {
		var (
			podifName string
			contID    string
			netconf   *types.NetConf
		)

		BeforeEach(func() {
			podifName = "net1"
			contID = "dummycid"
			netconf = &types.NetConf{
				IbSriovNetConf: types.IbSriovNetConf{
					Master:      "ib0",
					DeviceID:    "0000:af:06.0",
					VFID:        0,
					HostIFNames: "ib1",
					ContIFNames: "net1",
				},
			}
		})
		It("Assuming existing interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}
			fakeLink := &FakeLink{netlink.LinkAttrs{Index: 1000, Name: "dummylink"}}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(nil)
			sm := sriovManager{nLink: mocked}
			err := sm.ReleaseVF(netconf, podifName, contID, targetNetNS)
			Expect(err).NotTo(HaveOccurred())
		})
		It("Host name held as alt name is dropped before the rename back", func() {
			// Reproduced on hardware: an alt name equal to the host name added while the VF is in the
			// pod makes LinkSetName fail with EEXIST on DEL, stranding the VF with the pod GUID.
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}
			fakeLink := &FakeLink{netlink.LinkAttrs{Index: 1000, Name: "net1", AltNames: []string{"other", netconf.HostIFNames}}}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkDelAltName", fakeLink, netconf.HostIFNames).Return(nil)
			mocked.On("LinkSetName", fakeLink, netconf.HostIFNames).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(nil)
			sm := sriovManager{nLink: mocked}
			err := sm.ReleaseVF(netconf, podifName, contID, targetNetNS)
			Expect(err).NotTo(HaveOccurred())
			mocked.AssertCalled(GinkgoT(), "LinkDelAltName", fakeLink, netconf.HostIFNames)
			mocked.AssertNotCalled(GinkgoT(), "LinkDelAltName", fakeLink, "other")
			mocked.AssertCalled(GinkgoT(), "LinkSetName", fakeLink, netconf.HostIFNames)
		})
		It("Assuming non existing interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(nil, errors.New("not found"))
			sm := sriovManager{nLink: mocked}
			err := sm.ReleaseVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming failed to set interface down", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}
			fakeLink := &FakeLink{netlink.LinkAttrs{Index: 1000, Name: "dummylink"}}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.ReleaseVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming failed to move interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}
			fakeLink := &FakeLink{netlink.LinkAttrs{Index: 1000, Name: "dummylink"}}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.ReleaseVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming existing interface", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}
			fakeLink := &FakeLink{netlink.LinkAttrs{Index: 1000, Name: "dummylink"}}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.ReleaseVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
		It("Assuming failed to set interface up after moving", func() {
			targetNetNS := newFakeNs()
			mocked := &mocks.NetlinkManager{}

			fakeLink := &FakeLink{netlink.LinkAttrs{
				Index: 1000,
				Name:  "dummylink",
			}}

			mocked.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mocked.On("LinkByIndex", mock.AnythingOfType("int")).Return(fakeLink, nil)
			mocked.On("LinkSetDown", fakeLink).Return(nil)
			mocked.On("LinkSetName", fakeLink, mock.Anything).Return(nil)
			mocked.On("LinkSetNsFd", fakeLink, mock.AnythingOfType("int")).Return(nil)
			mocked.On("LinkSetUp", fakeLink).Return(errors.New("failed"))
			sm := sriovManager{nLink: mocked}
			err := sm.SetupVF(netconf, podifName, contID, targetNetNS)
			Expect(err).To(HaveOccurred())
		})
	})
	Context("Checking ResetVFConfig function", func() {
		var (
			netconf *types.NetConf
		)

		BeforeEach(func() {
			netconf = &types.NetConf{
				IbSriovNetConf: types.IbSriovNetConf{
					Master:      "i4",
					DeviceID:    "0000:af:06.0",
					VFID:        0,
					HostIFNames: "i1",
				},
			}
		})

		It("ResetVFConfig with valid GUID", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.HostIFGUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkSetName", fakeLink, netconf.HostIFNames).Return(nil)
			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkByIndex", mock.AnythingOfType("int")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)

			mockedPciUtils.On("RebindVf", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ResetVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
		})
		It("ResetVFConfig with valid GUID - host name held as alt name is dropped before the rename", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			// udev renamed the VF to its persistent name and recorded it as an alt name; the CNI
			// then renamed it. Renaming back would fail with EEXIST unless the alt name goes first.
			fakeLink := &FakeLink{netlink.LinkAttrs{Name: "ib0", AltNames: []string{netconf.HostIFNames, "other"}}}
			netconf.HostIFGUID = "01:23:45:67:89:ab:cd:ef"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkByIndex", mock.AnythingOfType("int")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkDelAltName", fakeLink, netconf.HostIFNames).Return(nil)
			mockedNetLinkManger.On("LinkSetName", fakeLink, netconf.HostIFNames).Return(nil)
			mockedPciUtils.On("RebindVf", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ResetVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
			mockedNetLinkManger.AssertCalled(GinkgoT(), "LinkDelAltName", fakeLink, netconf.HostIFNames)
			mockedNetLinkManger.AssertNotCalled(GinkgoT(), "LinkDelAltName", fakeLink, "other")
			mockedNetLinkManger.AssertCalled(GinkgoT(), "LinkSetName", fakeLink, netconf.HostIFNames)
		})
		It("ResetVFConfig without GUID", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkByIndex", mock.AnythingOfType("int")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ResetVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
		})
		It("ResetVFConfig with GUID all zeros", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.HostIFGUID = "00:00:00:00:00:00:00:00"

			mockedNetLinkManger.On("LinkSetName", fakeLink, netconf.HostIFNames).Return(nil)
			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkByIndex", mock.AnythingOfType("int")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkSetVfNodeGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)
			mockedNetLinkManger.On("LinkSetVfPortGUID", fakeLink, mock.AnythingOfType("int"), mock.Anything).Return(nil)

			mockedPciUtils.On("RebindVf", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ResetVFConfig(netconf)
			Expect(err).NotTo(HaveOccurred())
			Expect(netconf.HostIFGUID).To(Equal("FF:FF:FF:FF:FF:FF:FF:FF"))
		})
		It("ResetVFConfig with invalid GUID", func() {
			mockedNetLinkManger := &mocks.NetlinkManager{}
			mockedPciUtils := &mocks.PciUtils{}

			fakeLink := &FakeLink{netlink.LinkAttrs{}}
			netconf.HostIFGUID = "12312-123:434"

			mockedNetLinkManger.On("LinkByName", mock.AnythingOfType("string")).Return(fakeLink, nil)
			mockedNetLinkManger.On("LinkByIndex", mock.AnythingOfType("int")).Return(fakeLink, nil)

			sm := sriovManager{nLink: mockedNetLinkManger, utils: mockedPciUtils}
			err := sm.ResetVFConfig(netconf)
			Expect(err).To(HaveOccurred())
		})
	})
})
