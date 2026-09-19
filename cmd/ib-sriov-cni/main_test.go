package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containernetworking/cni/pkg/skel"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/stretchr/testify/mock"

	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/config"
	localtypes "github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types"
	"github.com/k8snetworkplumbingwg/ib-sriov-cni/pkg/types/mocks"
)

// fakeNetNS runs closures in the current namespace; enough for handleVFAdd because the manager is
// mocked in these tests.
type fakeNetNS struct{}

func (fakeNetNS) Do(toRun func(ns.NetNS) error) error { return toRun(fakeNetNS{}) }
func (fakeNetNS) Set() error                          { return nil }
func (fakeNetNS) Path() string                        { return "/fake/netns" }
func (fakeNetNS) Fd() uintptr                         { return 0 }
func (fakeNetNS) Close() error                        { return nil }

func testAddInputs() (*skel.CmdArgs, *localtypes.NetConf, *current.Result) {
	args := &skel.CmdArgs{ContainerID: "cid-test", IfName: "ibsriovtest0", StdinData: []byte(`{}`)}
	conf := &localtypes.NetConf{IbSriovNetConf: localtypes.IbSriovNetConf{
		Master: "ibFake0", DeviceID: "0000:af:06.0", VFID: 0, HostIFNames: "ibFake0v0",
		GUID: "02:00:00:00:00:20:3f:42",
	}}
	return args, conf, &current.Result{}
}

// pointCacheAt redirects the NetConf cache for one test.
func pointCacheAt(t *testing.T, dir string) {
	t.Helper()
	old := config.DefaultCNIDir
	config.DefaultCNIDir = dir
	t.Cleanup(func() { config.DefaultCNIDir = old })
}

func TestHandleVFAdd_ResetsVFWhenApplyVFConfigFails(t *testing.T) {
	// A failure inside ApplyVFConfig itself (e.g. the rebind after the GUID write) must be undone too.
	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(errors.New("rebind failed"))
	sm.On("ResetVFConfig", conf).Return(nil)

	err := handleVFAdd(sm, args, conf, fakeNetNS{}, result)
	if err == nil || !strings.Contains(err.Error(), "rebind failed") {
		t.Fatalf("expected the ApplyVFConfig error, got %v", err)
	}
	sm.AssertNumberOfCalls(t, "ResetVFConfig", 1)
	sm.AssertNotCalled(t, "SetupVF", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestHandleVFAdd_ResetsVFWhenSetupVFFails(t *testing.T) {
	// The VF netdev rename race surfaces here ("error getting VF netdevice with name ib0").
	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).Return(errors.New("error getting VF netdevice with name ib0"))
	sm.On("ResetVFConfig", conf).Return(nil)

	err := handleVFAdd(sm, args, conf, fakeNetNS{}, result)
	if err == nil || !strings.Contains(err.Error(), "ib0") {
		t.Fatalf("expected the SetupVF error, got %v", err)
	}
	sm.AssertNumberOfCalls(t, "ResetVFConfig", 1)
}

func TestHandleVFAdd_ResetsVFWhenCacheSaveFails(t *testing.T) {
	// The last step of the ADD. A path below a regular file makes MkdirAll fail deterministically.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointCacheAt(t, filepath.Join(blocker, "ib-sriov"))

	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).
		Run(func(mock.Arguments) { conf.ContIFNames = args.IfName }).Return(nil)
	sm.On("ReleaseVF", conf, args.IfName, args.ContainerID, mock.Anything).Return(nil)
	sm.On("ResetVFConfig", conf).Return(nil)

	err := handleVFAdd(sm, args, conf, fakeNetNS{}, result)
	if err == nil || !strings.Contains(err.Error(), "error saving NetConf") {
		t.Fatalf("expected the SaveNetConf error, got %v", err)
	}
	sm.AssertNumberOfCalls(t, "ResetVFConfig", 1)
	sm.AssertNumberOfCalls(t, "ReleaseVF", 1)
}

func TestHandleVFAdd_ReportsResetFailure(t *testing.T) {
	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(errors.New("rebind failed"))
	sm.On("ResetVFConfig", conf).Return(errors.New("restoreVFName error"))

	err := handleVFAdd(sm, args, conf, fakeNetNS{}, result)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"rebind failed", "resetting the VF after the failed ADD also failed", "restoreVFName error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestHandleVFAdd_DoesNotResetVFOnSuccess(t *testing.T) {
	dir := t.TempDir()
	pointCacheAt(t, dir)

	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).
		Run(func(mock.Arguments) { conf.ContIFNames = args.IfName }).Return(nil)

	if err := handleVFAdd(sm, args, conf, fakeNetNS{}, result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sm.AssertNotCalled(t, "ResetVFConfig", mock.Anything)
	if _, err := os.Stat(filepath.Join(dir, args.ContainerID+"-"+args.IfName)); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
}

func TestHandleVFAdd_SetupVFFailure_ReleasesVFAndReportsReleaseError(t *testing.T) {
	// Review P2: the cleanup used to live inside doVFConfig and discard ReleaseVF's error when
	// SetupVF failed. Now one defer in handleVFAdd covers that failure too and joins the error.
	args, conf, result := testAddInputs()
	currentName := "vfdev1001"
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).
		Run(func(mock.Arguments) { conf.ContIFNames = currentName }).Return(errors.New("pod rename failed"))
	sm.On("ReleaseVF", conf, currentName, args.ContainerID, mock.Anything).Return(errors.New("rename back failed"))
	sm.On("ResetVFConfig", conf).Return(nil)

	err := handleVFAdd(sm, args, conf, fakeNetNS{}, result)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"pod rename failed", "failed to release VF", "rename back failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	sm.AssertNumberOfCalls(t, "ReleaseVF", 1)
	sm.AssertNumberOfCalls(t, "ResetVFConfig", 1)
}

func TestHandleVFAdd_SetupVFFailure_ReleaseRunsBeforeReset(t *testing.T) {
	// The reset rebinds the VF and must find it back in the host netns: release first, reset last.
	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).
		Run(func(mock.Arguments) { conf.ContIFNames = args.IfName }).Return(errors.New("boom"))
	sm.On("ReleaseVF", conf, args.IfName, args.ContainerID, mock.Anything).Return(nil)
	sm.On("ResetVFConfig", conf).Return(nil)

	if err := handleVFAdd(sm, args, conf, fakeNetNS{}, result); err == nil {
		t.Fatal("expected an error")
	}
	order := make([]string, 0, len(sm.Calls))
	for _, c := range sm.Calls {
		order = append(order, c.Method)
	}
	want := []string{"ApplyVFConfig", "SetupVF", "ReleaseVF", "ResetVFConfig"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("call order %v, want %v", order, want)
	}
}

func TestHandleVFAdd_SetupVFFailure_NoReleaseWhenPodIfaceAbsent(t *testing.T) {
	// SetupVF failed before moving the interface (the usual case: netdev lookup or the udev wait):
	// there is nothing in the pod netns to release, only the reset runs.
	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).Return(errors.New("boom"))
	sm.On("ResetVFConfig", conf).Return(nil)

	if err := handleVFAdd(sm, args, conf, fakeNetNS{}, result); err == nil {
		t.Fatal("expected an error")
	}
	sm.AssertNotCalled(t, "ReleaseVF", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	sm.AssertNumberOfCalls(t, "ResetVFConfig", 1)
}

func TestHandleVFAdd_CacheSaveFailure_ReleasesVF(t *testing.T) {
	// After SetupVF succeeded the interface is in the pod netns; a later failure must release it.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointCacheAt(t, filepath.Join(blocker, "ib-sriov"))

	args, conf, result := testAddInputs()
	sm := &mocks.Manager{}
	sm.On("ApplyVFConfig", conf).Return(nil)
	sm.On("SetupVF", conf, args.IfName, args.ContainerID, mock.Anything).
		Run(func(mock.Arguments) { conf.ContIFNames = args.IfName }).Return(nil)
	sm.On("ReleaseVF", conf, args.IfName, args.ContainerID, mock.Anything).Return(nil)
	sm.On("ResetVFConfig", conf).Return(nil)

	if err := handleVFAdd(sm, args, conf, fakeNetNS{}, result); err == nil {
		t.Fatal("expected an error")
	}
	sm.AssertNumberOfCalls(t, "ReleaseVF", 1)
	sm.AssertNumberOfCalls(t, "ResetVFConfig", 1)
}
