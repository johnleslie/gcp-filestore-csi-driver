/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	mount "k8s.io/mount-utils"
	"sigs.k8s.io/gcp-filestore-csi-driver/pkg/cloud_provider/metadata"
)

var (
	testVolumeCapability = &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	testVolumeAttributes = map[string]string{
		attrIP:     "1.1.1.1",
		attrVolume: "test-volume",
	}
	testDevice = "1.1.1.1:/test-volume"

	testWindowsValidPath = "C:\\test"
	testWindowsSecrets   = map[string]string{
		optionSmbUser:     "foo",
		optionSmbPassword: "bar",
	}
	testWindowsDevice = "\\\\1.1.1.1\\test-volume"
)

type nodeServerTestEnv struct {
	ns csi.NodeServer
	fm *mount.FakeMounter
}

func initTestNodeServer(t *testing.T) *nodeServerTestEnv {
	// TODO: make a constructor in FakeMmounter library
	mounter := &mount.FakeMounter{MountPoints: []mount.MountPoint{}}
	metaserice, err := metadata.NewFakeService()
	if err != nil {
		t.Fatalf("Failed to init metadata service")
	}
	return &nodeServerTestEnv{
		ns: newNodeServer(initTestDriver(t), mounter, metaserice),
		fm: mounter,
	}
}

func TestNodePublishVolume(t *testing.T) {
	defaultPerm := os.FileMode(0750) + os.ModeDir

	// Setup mount target path
	base, err := ioutil.TempDir("", "node-publish-")
	if err != nil {
		t.Fatalf("failed to setup testdir: %v", err)
	}
	testTargetPath := filepath.Join(base, "mount")
	if err = os.MkdirAll(testTargetPath, defaultPerm); err != nil {
		t.Fatalf("failed to setup target path: %v", err)
	}
	stagingTargetPath := filepath.Join(base, "staging")
	defer os.RemoveAll(base)
	cases := []struct {
		name          string
		mounts        []mount.MountPoint // already existing mounts
		req           *csi.NodePublishVolumeRequest
		actions       []mount.FakeAction
		expectedMount *mount.MountPoint
		expectErr     bool
	}{
		{
			name:      "empty request",
			req:       &csi.NodePublishVolumeRequest{},
			expectErr: true,
		},
		{
			name: "valid request not already mounted",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:          testVolumeID,
				StagingTargetPath: stagingTargetPath,
				TargetPath:        testTargetPath,
				VolumeCapability:  testVolumeCapability,
				VolumeContext:     testVolumeAttributes,
			},
			actions:       []mount.FakeAction{{Action: mount.FakeActionMount}},
			expectedMount: &mount.MountPoint{Device: stagingTargetPath, Path: testTargetPath, Type: "nfs", Opts: []string{"bind"}},
		},
		{
			name:   "valid request already mounted",
			mounts: []mount.MountPoint{{Device: "/test-device", Path: testTargetPath}},
			req: &csi.NodePublishVolumeRequest{
				VolumeId:          testVolumeID,
				StagingTargetPath: stagingTargetPath,
				TargetPath:        testTargetPath,
				VolumeCapability:  testVolumeCapability,
				VolumeContext:     testVolumeAttributes,
			},
			expectedMount: &mount.MountPoint{Device: "/test-device", Path: testTargetPath},
		},
		{
			name: "valid request with user mount options",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:          testVolumeID,
				StagingTargetPath: stagingTargetPath,
				TargetPath:        testTargetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							MountFlags: []string{"foo", "bar"},
						},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
				VolumeContext: testVolumeAttributes,
			},
			actions: []mount.FakeAction{{Action: mount.FakeActionMount}},

			expectedMount: &mount.MountPoint{Device: stagingTargetPath, Path: testTargetPath, Type: "nfs", Opts: []string{"bind", "foo", "bar"}},
		},
		{
			name: "valid request read only",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:          testVolumeID,
				StagingTargetPath: stagingTargetPath,
				TargetPath:        testTargetPath,
				VolumeCapability:  testVolumeCapability,
				VolumeContext:     testVolumeAttributes,
				Readonly:          true,
			},
			actions:       []mount.FakeAction{{Action: mount.FakeActionMount}},
			expectedMount: &mount.MountPoint{Device: stagingTargetPath, Path: testTargetPath, Type: "nfs", Opts: []string{"bind", "ro"}},
		},
		{
			name: "empty target path",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:          testVolumeID,
				StagingTargetPath: stagingTargetPath,
				VolumeCapability:  testVolumeCapability,
				VolumeContext:     testVolumeAttributes,
			},
			expectErr: true,
		},
		{
			name: "empty staging target path",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:         testVolumeID,
				TargetPath:       testTargetPath,
				VolumeCapability: testVolumeCapability,
				VolumeContext:    testVolumeAttributes,
			},
			expectErr: true,
		},
		{
			name: "invalid volume capability",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:      testVolumeID,
				TargetPath:    testTargetPath,
				VolumeContext: testVolumeAttributes,
			},
			expectErr: true,
		},
		{
			name: "invalid volume attribute",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:         testVolumeID,
				TargetPath:       testTargetPath,
				VolumeCapability: testVolumeCapability,
			},
			expectErr: true,
		},
		// TODO: Revisit this (https://github.com/kubernetes-sigs/gcp-filestore-csi-driver/issues/47).
		// {
		// 	name: "target path doesn't exist",
		// 	req: &csi.NodePublishVolumeRequest{
		// 		VolumeId:         testVolumeID,
		// 		TargetPath:       "/node-publish-test-not-exists",
		// 		VolumeCapability: testVolumeCapability,
		// 		VolumeContext:    testVolumeAttributes,
		// 	},
		// 	expectErr: true,
		// },
		// TODO add a test case for mount failure.
		// need to modify FakeMounter to be able to fail the mount call (and unmount)
	}

	for _, test := range cases {
		testEnv := initTestNodeServer(t)
		if test.mounts != nil {
			testEnv.fm.MountPoints = test.mounts
		}

		_, err = testEnv.ns.NodePublishVolume(context.TODO(), test.req)
		if !test.expectErr && err != nil {
			t.Errorf("test %q failed: %v", test.name, err)
		}
		if test.expectErr && err == nil {
			t.Errorf("test %q failed: got success", test.name)
		}

		validateMountPoint(t, test.name, testEnv.fm, test.expectedMount)
		// TODO: ValidateMountActions if possible.
	}
}

// TODO: Revisit windows tests
func testWindowsNodePublishVolume(t *testing.T) {
	defaultPerm := os.FileMode(0750) + os.ModeDir
	defaultOsString := goOs

	// Setup mount target path
	base, err := ioutil.TempDir("", "node-publish-")
	if err != nil {
		t.Fatalf("failed to setup testdir: %v", err)
	}
	testTargetPath := filepath.Join(base, "mount")
	if err = os.MkdirAll(testTargetPath, defaultPerm); err != nil {
		t.Fatalf("failed to setup target path: %v", err)
	}
	defer os.RemoveAll(base)

	goOs = "windows"

	cases := []struct {
		name          string
		mounts        []mount.MountPoint // already existing mounts
		req           *csi.NodePublishVolumeRequest
		actions       []mount.FakeAction
		expectedMount *mount.MountPoint
		expectErr     bool
	}{
		// TODO: enable this test after https://github.com/kubernetes/kubernetes/issues/81609

		// {
		// 	name:     "windows target path does exist",
		// 	req: &csi.NodePublishVolumeRequest{
		// 		VolumeId:         testVolumeID,
		// 		TargetPath:       testTargetPath,
		// 		VolumeCapability: testVolumeCapability,
		// 		VolumeAttributes: testVolumeAttributes,
		// 		NodePublishSecrets: testWindowsSecrets
		// 	},
		// 	expectErr: true,
		// },
		{
			name: "windows target path doesn't exist",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:         testVolumeID,
				TargetPath:       testWindowsValidPath,
				VolumeCapability: testVolumeCapability,
				VolumeContext:    testVolumeAttributes,
				Secrets:          testWindowsSecrets,
			},

			actions:       []mount.FakeAction{{Action: mount.FakeActionMount}},
			expectedMount: &mount.MountPoint{Device: testWindowsDevice, Path: testWindowsValidPath, Type: "cifs", Opts: []string{"foo", "bar"}},
		},
		{
			name: "windows no user",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:         testVolumeID,
				TargetPath:       testWindowsValidPath,
				VolumeCapability: testVolumeCapability,
				VolumeContext:    testVolumeAttributes,
				Secrets: map[string]string{
					optionSmbPassword: "bar",
				},
			},
			expectErr: true,
		},
		{
			name: "windows no password",
			req: &csi.NodePublishVolumeRequest{
				VolumeId:         testVolumeID,
				TargetPath:       testWindowsValidPath,
				VolumeCapability: testVolumeCapability,
				VolumeContext:    testVolumeAttributes,
				Secrets: map[string]string{
					optionSmbUser: "foo",
				},
			},
			expectErr: true,
		},
	}

	for _, test := range cases {
		testEnv := initTestNodeServer(t)
		if test.mounts != nil {
			testEnv.fm.MountPoints = test.mounts
		}

		_, err = testEnv.ns.NodePublishVolume(context.TODO(), test.req)
		if !test.expectErr && err != nil {
			t.Errorf("test %q failed: %v", test.name, err)
		}
		if test.expectErr && err == nil {
			t.Errorf("test %q failed: got success", test.name)
		}

		validateMountPoint(t, test.name, testEnv.fm, test.expectedMount)
		// TODO: ValidateMountActions if possible.
	}
	goOs = defaultOsString
}

func TestNodeUnpublishVolume(t *testing.T) {
	defaultPerm := os.FileMode(0750) + os.ModeDir

	// Setup mount target path
	base, err := ioutil.TempDir("", "node-publish-")
	if err != nil {
		t.Fatalf("failed to setup testdir: %v", err)
	}
	testTargetPath := filepath.Join(base, "mount")
	if err = os.MkdirAll(testTargetPath, defaultPerm); err != nil {
		t.Fatalf("failed to setup target path: %v", err)
	}
	defer os.RemoveAll(base)

	cases := []struct {
		name          string
		mounts        []mount.MountPoint // already existing mounts
		req           *csi.NodeUnpublishVolumeRequest
		actions       []mount.FakeAction
		expectedMount *mount.MountPoint
		expectErr     bool
	}{
		{
			name:   "successful unmount",
			mounts: []mount.MountPoint{{Device: testDevice, Path: testTargetPath}},
			req: &csi.NodeUnpublishVolumeRequest{
				VolumeId:   testVolumeID,
				TargetPath: testTargetPath,
			},
			actions: []mount.FakeAction{{Action: mount.FakeActionUnmount}},
		},
		{
			name: "empty target path",
			req: &csi.NodeUnpublishVolumeRequest{
				VolumeId: testVolumeID,
			},
			expectErr: true,
		},
		{
			name: "dir doesn't exist",
			req: &csi.NodeUnpublishVolumeRequest{
				VolumeId:   testVolumeID,
				TargetPath: "/node-unpublish-dir-not-exists",
			},
		},
		{
			name: "dir not mounted",
			req: &csi.NodeUnpublishVolumeRequest{
				VolumeId:   testVolumeID,
				TargetPath: testTargetPath,
			},
		},
		// TODO:
		// mount check failed
		// unmount failed
	}

	for _, test := range cases {
		testEnv := initTestNodeServer(t)
		if test.mounts != nil {
			testEnv.fm.MountPoints = test.mounts
		}

		_, err = testEnv.ns.NodeUnpublishVolume(context.TODO(), test.req)
		if !test.expectErr && err != nil {
			t.Errorf("test %q failed: %v", test.name, err)
		}
		if test.expectErr && err == nil {
			t.Errorf("test %q failed: got success", test.name)
		}

		validateMountPoint(t, test.name, testEnv.fm, test.expectedMount)
		// TODO: ValidateMountActions if possible.
	}
}

func TestValidateVolumeAttributes(t *testing.T) {
	cases := []struct {
		name      string
		attrs     map[string]string
		expectErr bool
	}{
		{
			name: "valid attributes",
			attrs: map[string]string{
				attrIP:     "1.1.1.1",
				attrVolume: "vol1",
			},
		},
		{
			name: "invalid ip",
			attrs: map[string]string{
				attrVolume: "vol1",
			},
			expectErr: true,
		},
		{
			name: "invalid volume",
			attrs: map[string]string{
				attrIP: "1.1.1.1",
			},
			expectErr: true,
		},
	}

	for _, test := range cases {
		err := validateVolumeAttributes(test.attrs)
		if !test.expectErr && err != nil {
			t.Errorf("test %q failed: %v", test.name, err)
		}
		if test.expectErr && err == nil {
			t.Errorf("test %q failed: got success", test.name)
		}
	}
}

// TODO
func TestNodeGetId(t *testing.T) {
}

// TODO
func TestNodeGetInfo(t *testing.T) {
}

// TODO
func TestNodeGetCapabilities(t *testing.T) {
}

func validateMountPoint(t *testing.T, name string, fm *mount.FakeMounter, e *mount.MountPoint) {
	if e == nil {
		if len(fm.MountPoints) != 0 {
			t.Errorf("test %q failed: got mounts %+v, expected none", name, fm.MountPoints)
		}
		return
	}

	if mLen := len(fm.MountPoints); mLen != 1 {
		t.Errorf("test %q failed: got %v mounts(%+v), expected %v", name, mLen, fm.MountPoints, 1)
		return
	}

	a := &fm.MountPoints[0]
	if a.Device != e.Device {
		t.Errorf("test %q failed: got device %q, expected %q", name, a.Device, e.Device)
	}
	if a.Path != e.Path {
		t.Errorf("test %q failed: got path %q, expected %q", name, a.Path, e.Path)
	}
	if a.Type != e.Type {
		t.Errorf("test %q failed: got type %q, expected %q", name, a.Type, e.Type)
	}

	// TODO: why does DeepEqual not work???
	aLen := len(a.Opts)
	eLen := len(e.Opts)
	if aLen != eLen {
		t.Errorf("test %q failed: got opts length %v, expected %v", name, aLen, eLen)
	} else {
		for i := range a.Opts {
			aOpt := a.Opts[i]
			eOpt := e.Opts[i]
			if aOpt != eOpt {
				t.Errorf("test %q failed: got opt %q, expected %q", name, aOpt, eOpt)
			}
		}
	}
}

type FakeBlockingMounter struct {
	*mount.FakeMounter
	// 'OperationUnblocker' channel is used to block the execution of the respective function using it. This is done by sending a channel of empty struct over 'OperationUnblocker' channel, and wait until the tester gives a go-ahead to proceed further in the execution of the function.
	OperationUnblocker chan chan struct{}
}

func NewFakeBlockingMounter(operationUnblocker chan chan struct{}) *FakeBlockingMounter {
	return &FakeBlockingMounter{
		FakeMounter:        &mount.FakeMounter{MountPoints: []mount.MountPoint{}},
		OperationUnblocker: operationUnblocker,
	}
}

func (m *FakeBlockingMounter) Mount(source string, target string, fstype string, options []string) error {
	execute := make(chan struct{})
	m.OperationUnblocker <- execute
	<-execute
	return m.FakeMounter.Mount(source, target, fstype, options)
}

func initBlockingTestNodeServer(t *testing.T, operationUnblocker chan chan struct{}) *nodeServerTestEnv {
	mounter := NewFakeBlockingMounter(operationUnblocker)
	metaserice, err := metadata.NewFakeService()
	if err != nil {
		t.Fatalf("Failed to init metadata service")
	}
	return &nodeServerTestEnv{
		ns: newNodeServer(initTestDriver(t), mounter, metaserice),
		fm: nil,
	}
}

type NodeRequestConfig struct {
	NodePublishReq   *csi.NodePublishVolumeRequest
	NodeUnpublishReq *csi.NodeUnpublishVolumeRequest
	NodeStageReq     *csi.NodeStageVolumeRequest
	NodeUnstageReq   *csi.NodeUnstageVolumeRequest
}

func TestConcurrentMounts(t *testing.T) {
	// A channel of size 1 is sufficient, because the caller of runRequest() in below steps immediately blocks and retrieves the channel of empty struct from 'operationUnblocker' channel. The test steps are such that, atmost one function pushes items on the 'operationUnblocker' channel, to indicate that the function is blocked and waiting for a signal to proceed futher in the execution.
	operationUnblocker := make(chan chan struct{}, 1)
	ns := initBlockingTestNodeServer(t, operationUnblocker)
	basePath, err := ioutil.TempDir("", "node-publish-")
	if err != nil {
		t.Fatalf("failed to setup testdir: %v", err)
	}
	stagingTargetPath := filepath.Join(basePath, "staging")
	targetPath1 := filepath.Join(basePath, "target1")
	targetPath2 := filepath.Join(basePath, "target2")

	runRequest := func(req *NodeRequestConfig) <-chan error {
		resp := make(chan error)
		go func() {
			var err error
			if req.NodePublishReq != nil {
				_, err = ns.ns.NodePublishVolume(context.Background(), req.NodePublishReq)
			} else if req.NodeUnpublishReq != nil {
				_, err = ns.ns.NodeUnpublishVolume(context.Background(), req.NodeUnpublishReq)
			} else if req.NodeStageReq != nil {
				_, err = ns.ns.NodeStageVolume(context.Background(), req.NodeStageReq)
			} else if req.NodeUnstageReq != nil {
				_, err = ns.ns.NodeUnstageVolume(context.Background(), req.NodeUnstageReq)
			}
			resp <- err
		}()
		return resp
	}

	// Node stage blocked after lock acquire.
	resp := runRequest(&NodeRequestConfig{
		NodeStageReq: &csi.NodeStageVolumeRequest{
			VolumeId:          testVolumeID,
			StagingTargetPath: stagingTargetPath,
			VolumeCapability:  testVolumeCapability,
			VolumeContext:     testVolumeAttributes,
		},
	})
	nodestageOpUnblocker := <-operationUnblocker

	// Same volume ID node stage should fail to acquire lock and return Aborted error.
	stageResp2 := runRequest(&NodeRequestConfig{
		NodeStageReq: &csi.NodeStageVolumeRequest{
			VolumeId:          testVolumeID,
			StagingTargetPath: stagingTargetPath,
			VolumeCapability:  testVolumeCapability,
			VolumeContext:     testVolumeAttributes,
		},
	})
	ValidateExpectedError(t, stageResp2, operationUnblocker, codes.Aborted)

	// Same volume ID node unstage should fail to acquire lock and return Aborted error.
	unstageResp := runRequest(&NodeRequestConfig{
		NodeUnstageReq: &csi.NodeUnstageVolumeRequest{
			VolumeId:          testVolumeID,
			StagingTargetPath: stagingTargetPath,
		},
	})
	ValidateExpectedError(t, unstageResp, operationUnblocker, codes.Aborted)

	// Unblock first node stage. Success expected.
	nodestageOpUnblocker <- struct{}{}
	if err := <-resp; err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Node publish blocked after lock acquire on the 'targetPath1'.
	targetPath1Publishresp := runRequest(&NodeRequestConfig{
		NodePublishReq: &csi.NodePublishVolumeRequest{
			VolumeId:          testVolumeID,
			StagingTargetPath: stagingTargetPath,
			TargetPath:        targetPath1,
			VolumeCapability:  testVolumeCapability,
			VolumeContext:     testVolumeAttributes,
		},
	})
	nodepublishOpTargetPath1Unblocker := <-operationUnblocker

	// Node publish for the same target path should fail to acquire lock and return Aborted error.
	targetPath1Publishresp2 := runRequest(&NodeRequestConfig{
		NodePublishReq: &csi.NodePublishVolumeRequest{
			VolumeId:          testVolumeID,
			StagingTargetPath: stagingTargetPath,
			TargetPath:        targetPath1,
			VolumeCapability:  testVolumeCapability,
			VolumeContext:     testVolumeAttributes,
		},
	})
	ValidateExpectedError(t, targetPath1Publishresp2, operationUnblocker, codes.Aborted)

	// Node unpublish for the same target path should fail to acquire lock and return Aborted error.
	targetPath1Unpublishresp := runRequest(&NodeRequestConfig{
		NodeUnpublishReq: &csi.NodeUnpublishVolumeRequest{
			VolumeId:   testVolumeID,
			TargetPath: targetPath1,
		},
	})
	ValidateExpectedError(t, targetPath1Unpublishresp, operationUnblocker, codes.Aborted)

	// Node publish succeeds for a second target path 'targetPath2'.
	targetPath2Publishresp2 := runRequest(&NodeRequestConfig{
		NodePublishReq: &csi.NodePublishVolumeRequest{
			VolumeId:          testVolumeID,
			StagingTargetPath: stagingTargetPath,
			TargetPath:        targetPath2,
			VolumeCapability:  testVolumeCapability,
			VolumeContext:     testVolumeAttributes,
		},
	})
	nodepublishOpTargetPath2Unblocker := <-operationUnblocker
	nodepublishOpTargetPath2Unblocker <- struct{}{}
	if err := <-targetPath2Publishresp2; err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Node unpublish succeeds for second target path.
	targetPath2Unpublishresp := runRequest(&NodeRequestConfig{
		NodeUnpublishReq: &csi.NodeUnpublishVolumeRequest{
			VolumeId:   testVolumeID,
			TargetPath: targetPath2,
		},
	})
	if err := <-targetPath2Unpublishresp; err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Unblock first node publish, and success expected.
	nodepublishOpTargetPath1Unblocker <- struct{}{}
	if err := <-targetPath1Publishresp; err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}
