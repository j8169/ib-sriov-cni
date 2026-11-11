package config

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types"
	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/utils"
)

var _ = Describe("Config", func() {
	Context("Checking LoadDeviceInfo function", func() {
		It("Records the VF netdev name once udev has finished with it", func() {
			conf := &types.NetConf{IbSriovNetConf: types.IbSriovNetConf{DeviceID: "0000:af:06.1"}}
			Expect(LoadDeviceInfo(conf)).To(Succeed())
			Expect(conf.HostIFNames).To(Equal("ib2"))
			Expect(conf.Master).To(Equal("ib0"))
		})
		It("Fails when the VF netdev cannot be found rather than reading a name blind", func() {
			// GetVFLinkIndex already retries the not-yet-registered states; if it still fails, the
			// name must not be read without the udev wait.
			vfDir := filepath.Join(utils.SysBusPci, "0000:af:06.0", "net")
			Expect(os.Rename(vfDir, vfDir+".hidden")).To(Succeed())
			DeferCleanup(func() { _ = os.Rename(vfDir+".hidden", vfDir) })
			conf := &types.NetConf{IbSriovNetConf: types.IbSriovNetConf{
				DeviceID: "0000:af:06.0", VFNetdevLookupAttempts: 2, VFNetdevLookupInterval: "1ms"}}
			err := LoadDeviceInfo(conf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to detect VF"))
			Expect(conf.HostIFNames).To(BeEmpty())
		})
		It("Fails the ADD while udev is still renaming the VF netdev", func() {
			// The recorded name is what DEL renames the VF back to; caching the kernel default mid-rename
			// would undo udev's persistent name later. Nothing has been written yet, so failing is safe.
			rec := filepath.Join(utils.UdevDataDir, "n1002")
			orig, err := os.ReadFile(rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(rec, []byte("E:ID_RENAMING=1\nV:1\nI:1789340000000002\nE:ID_NET_NAME=ib2\n"), 0o644)).To(Succeed())
			DeferCleanup(func() { _ = os.WriteFile(rec, orig, 0o644) })
			conf := &types.NetConf{IbSriovNetConf: types.IbSriovNetConf{DeviceID: "0000:af:06.1", UdevSettleTimeout: "1s"}}
			err = LoadDeviceInfo(conf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("udev did not finish"))
			Expect(conf.HostIFNames).To(BeEmpty())
		})
	})
	Context("Checking LoadConf function", func() {
		It("Rejects invalid netdev timing options", func() {
			for _, body := range []string{
				`{"name":"n","type":"ib-sriov","udevSettleTimeout":"soon"}`,
				`{"name":"n","type":"ib-sriov","udevSettleTimeout":"-1s"}`,
				`{"name":"n","type":"ib-sriov","udevSettleTimeout":"0"}`,     // the wait cannot be disabled
				`{"name":"n","type":"ib-sriov","udevSettleTimeout":"999ms"}`, // below MinUdevSettleTimeout
				`{"name":"n","type":"ib-sriov","udevSettlePoll":"0"}`,
				`{"name":"n","type":"ib-sriov","vfNetdevLookupInterval":"5"}`,
				`{"name":"n","type":"ib-sriov","vfNetdevLookupAttempts":-1}`,
			} {
				_, err := LoadConf([]byte(body))
				Expect(err).To(HaveOccurred(), body)
			}
		})
		It("Accepts and applies netdev timing options", func() {
			conf, err := LoadConf([]byte(`{"name":"n","type":"ib-sriov","udevSettleTimeout":"1s","udevSettlePoll":"10ms","vfNetdevLookupAttempts":5,"vfNetdevLookupInterval":"20ms"}`))
			Expect(err).NotTo(HaveOccurred())
			w, err := conf.NetdevWait()
			Expect(err).NotTo(HaveOccurred())
			Expect(w).To(Equal(types.NetdevWait{LookupAttempts: 5, LookupInterval: 20 * time.Millisecond, UdevTimeout: time.Second, UdevPoll: 10 * time.Millisecond}))
			w, err = (&types.NetConf{}).NetdevWait()
			Expect(err).NotTo(HaveOccurred())
			Expect(w).To(Equal(types.DefaultNetdevWait))
		})
		It("Treats zero lookup attempts as the default", func() {
			conf, err := LoadConf([]byte(`{"name":"n","type":"ib-sriov","vfNetdevLookupAttempts":0}`))
			Expect(err).NotTo(HaveOccurred())
			w, err := conf.NetdevWait()
			Expect(err).NotTo(HaveOccurred())
			Expect(w.LookupAttempts).To(Equal(types.DefaultNetdevWait.LookupAttempts))
		})
		It("Assuming correct config file - existing DeviceID", func() {
			conf := []byte(`{
        "name": "mynet",
        "type": "ib-sriov",
        "deviceID": "0000:af:06.1",
        "vf": 0,
        "ipam": {
            "type": "host-local",
            "subnet": "10.55.206.0/26",
            "routes": [
                { "dst": "0.0.0.0/0" }
            ],
            "gateway": "10.55.206.1"
        }
                        }`)
			_, err := LoadConf(conf)
			Expect(err).NotTo(HaveOccurred())
		})
		It("Assuming incorrect config file - broken json", func() {
			conf := []byte(`{
        "name": "mynet"
		"type": "ib-sriov",
		"deviceID": "0000:af:06.1",
        "vf": 0,
        "ipam": {
            "type": "host-local",
            "subnet": "10.55.206.0/26",
            "routes": [
                { "dst": "0.0.0.0/0" }
            ],
            "gateway": "10.55.206.1"
        }
                        }`)
			_, err := LoadConf(conf)
			Expect(err).To(HaveOccurred())
		})
	})
	Context("Checking getVfInfo function", func() {
		It("Assuming existing PF", func() {
			_, _, err := getVfInfo("0000:af:06.0")
			Expect(err).NotTo(HaveOccurred())
		})
		It("Assuming not existing PF", func() {
			_, _, err := getVfInfo("0000:af:07.0")
			Expect(err).To(HaveOccurred())
		})
	})
})
