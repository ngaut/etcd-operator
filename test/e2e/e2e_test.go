// Copyright 2016 The etcd-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/coreos/etcd-operator/pkg/cluster"
	"github.com/coreos/etcd-operator/pkg/spec"
	"github.com/coreos/etcd-operator/pkg/util/constants"
	"github.com/coreos/etcd-operator/pkg/util/k8sutil"
	"github.com/coreos/etcd-operator/test/e2e/framework"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	k8sclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/util/wait"
)

func TestCreateCluster(t *testing.T) {
	f := framework.Global
	testEtcd, err := createEtcdCluster(f, makeEtcdCluster("test-etcd-", 3))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := deleteEtcdCluster(f, testEtcd.Name); err != nil {
			t.Fatal(err)
		}
	}()

	if _, err := waitUntilSizeReached(f, testEtcd.Name, 3, 60); err != nil {
		t.Fatalf("failed to create 3 members etcd cluster: %v", err)
	}
}

func TestResizeCluster3to5(t *testing.T) {
	f := framework.Global
	testEtcd, err := createEtcdCluster(f, makeEtcdCluster("test-etcd-", 3))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := deleteEtcdCluster(f, testEtcd.Name); err != nil {
			t.Fatal(err)
		}
	}()

	if _, err := waitUntilSizeReached(f, testEtcd.Name, 3, 60); err != nil {
		t.Fatalf("failed to create 3 members etcd cluster: %v", err)
		return
	}
	fmt.Println("reached to 3 members cluster")

	testEtcd.Spec.Size = 5
	if err := updateEtcdCluster(f, testEtcd); err != nil {
		t.Fatal(err)
	}

	if _, err := waitUntilSizeReached(f, testEtcd.Name, 5, 60); err != nil {
		t.Fatalf("failed to resize to 5 members etcd cluster: %v", err)
	}
}

func TestResizeCluster5to3(t *testing.T) {
	f := framework.Global
	testEtcd, err := createEtcdCluster(f, makeEtcdCluster("test-etcd-", 5))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := deleteEtcdCluster(f, testEtcd.Name); err != nil {
			t.Fatal(err)
		}
	}()

	if _, err := waitUntilSizeReached(f, testEtcd.Name, 5, 90); err != nil {
		t.Fatalf("failed to create 5 members etcd cluster: %v", err)
		return
	}
	fmt.Println("reached to 5 members cluster")

	testEtcd.Spec.Size = 3
	if err := updateEtcdCluster(f, testEtcd); err != nil {
		t.Fatal(err)
	}

	if _, err := waitUntilSizeReached(f, testEtcd.Name, 3, 60); err != nil {
		t.Fatalf("failed to resize to 3 members etcd cluster: %v", err)
	}
}

func TestOneMemberRecovery(t *testing.T) {
	f := framework.Global
	testEtcd, err := createEtcdCluster(f, makeEtcdCluster("test-etcd-", 3))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := deleteEtcdCluster(f, testEtcd.Name); err != nil {
			t.Fatal(err)
		}
	}()

	names, err := waitUntilSizeReached(f, testEtcd.Name, 3, 60)
	if err != nil {
		t.Fatalf("failed to create 3 members etcd cluster: %v", err)
		return
	}
	fmt.Println("reached to 3 members cluster")

	if err := killMembers(f, names[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := waitUntilSizeReached(f, testEtcd.Name, 3, 60); err != nil {
		t.Fatalf("failed to resize to 3 members etcd cluster: %v", err)
	}
}

// TestDisasterRecovery2Members tests disaster recovery that
// ooperator will make a backup from the left one pod.
func TestDisasterRecovery2Members(t *testing.T) {
	testDisasterRecovery(t, 2)
}

// TestDisasterRecoveryAll tests disaster recovery that
// we should make a backup ahead and ooperator will recover cluster from it.
func TestDisasterRecoveryAll(t *testing.T) {
	testDisasterRecovery(t, 3)
}

func testDisasterRecovery(t *testing.T, numToKill int) {
	f := framework.Global
	backupPolicy := &spec.BackupPolicy{
		SnapshotIntervalInSecond: 60 * 60,
		MaxSnapshot:              5,
		VolumeSizeInMB:           512,
		StorageType:              spec.BackupStorageTypePersistentVolume,
		CleanupBackupIfDeleted:   true,
	}
	origEtcd := makeEtcdCluster("test-etcd-", 3)
	origEtcd = etcdClusterWithBackup(origEtcd, backupPolicy)
	testEtcd, err := createEtcdCluster(f, origEtcd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := deleteEtcdCluster(f, testEtcd.Name); err != nil {
			t.Fatal(err)
		}
		// TODO: add checking of removal of backup pod
	}()

	names, err := waitUntilSizeReached(f, testEtcd.Name, 3, 60)
	if err != nil {
		t.Fatalf("failed to create 3 members etcd cluster: %v", err)
	}
	fmt.Println("reached to 3 members cluster")
	if err := waitBackupPodUp(f, testEtcd.Name, 60*time.Second); err != nil {
		t.Fatalf("failed to create backup pod: %v", err)
	}
	// No left pod to make a backup from. We need to back up ahead.
	// If there is any left pod, ooperator should be able to make a backup from it.
	if numToKill == len(names) {
		if err := makeBackup(f, testEtcd.Name); err != nil {
			t.Fatalf("fail to make a latest backup: %v", err)
		}
	}
	toKill := make([]string, numToKill)
	for i := 0; i < numToKill; i++ {
		toKill[i] = names[i]
	}
	// TODO: There might be race that ooperator will recover members between
	// 		these members are deleted individually.
	if err := killMembers(f, toKill...); err != nil {
		t.Fatal(err)
	}
	if _, err := waitUntilSizeReached(f, testEtcd.Name, 3, 120); err != nil {
		t.Fatalf("failed to resize to 3 members etcd cluster: %v", err)
	}
	// TODO: add checking of data in etcd
}

func waitBackupPodUp(f *framework.Framework, clusterName string, timeout time.Duration) error {
	return wait.Poll(5*time.Second, timeout, func() (done bool, err error) {
		podList, err := f.KubeClient.Pods(f.Namespace.Name).List(api.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app":          k8sutil.BackupPodSelectorAppField,
				"etcd_cluster": clusterName,
			})})
		if err != nil {
			return false, err
		}
		return len(podList.Items) > 0, nil
	})
}

func makeBackup(f *framework.Framework, clusterName string) error {
	svc, err := f.KubeClient.Services(f.Namespace.Name).Get(k8sutil.MakeBackupName(clusterName))
	if err != nil {
		return err
	}
	// In our test environment, we assume kube-proxy should be running on the same node.
	// Thus we can use the service IP.
	ok := cluster.RequestBackupNow(f.KubeClient.Client, fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, constants.DefaultBackupPodHTTPPort))
	if !ok {
		return fmt.Errorf("fail to request backupnow")
	}
	return nil
}

func waitUntilSizeReached(f *framework.Framework, clusterName string, size, timeout int) ([]string, error) {
	return waitSizeReachedWithFilter(f, clusterName, size, timeout, func(*api.Pod) bool { return true })
}

func waitSizeReachedWithFilter(f *framework.Framework, clusterName string, size, timeout int, filterPod func(*api.Pod) bool) ([]string, error) {
	var names []string
	err := wait.Poll(5*time.Second, time.Duration(timeout)*time.Second, func() (done bool, err error) {
		podList, err := f.KubeClient.Pods(f.Namespace.Name).List(k8sutil.EtcdPodListOpt(clusterName))
		if err != nil {
			return false, err
		}
		names = nil
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Status.Phase == api.PodRunning {
				names = append(names, pod.Name)
			}
		}
		fmt.Printf("waiting size (%d), etcd pods: %v\n", size, names)
		if len(names) != size {
			return false, nil
		}
		// TODO: check etcd member membership
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

func killMembers(f *framework.Framework, names ...string) error {
	for _, name := range names {
		err := f.KubeClient.Pods(f.Namespace.Name).Delete(name, api.NewDeleteOptions(0))
		if err != nil {
			return err
		}
	}
	return nil
}

func makeEtcdCluster(genName string, size int) *spec.EtcdCluster {
	return &spec.EtcdCluster{
		TypeMeta: unversioned.TypeMeta{
			Kind:       "EtcdCluster",
			APIVersion: "coreos.com/v1",
		},
		ObjectMeta: api.ObjectMeta{
			GenerateName: genName,
		},
		Spec: spec.ClusterSpec{
			Size: size,
		},
	}
}

func etcdClusterWithBackup(ec *spec.EtcdCluster, backupPolicy *spec.BackupPolicy) *spec.EtcdCluster {
	ec.Spec.Backup = backupPolicy
	return ec
}
func etcdClusterWithVersion(ec *spec.EtcdCluster, version string) *spec.EtcdCluster {
	ec.Spec.Version = version
	return ec
}

func createEtcdCluster(f *framework.Framework, e *spec.EtcdCluster) (*spec.EtcdCluster, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	resp, err := f.KubeClient.Client.Post(
		fmt.Sprintf("%s/apis/coreos.com/v1/namespaces/%s/etcdclusters", f.MasterHost, f.Namespace.Name),
		"application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status: %v", resp.Status)
	}
	decoder := json.NewDecoder(resp.Body)
	res := &spec.EtcdCluster{}
	if err := decoder.Decode(res); err != nil {
		return nil, err
	}
	fmt.Printf("created etcd cluster: %v\n", res.Name)
	return res, nil
}

func updateEtcdCluster(f *framework.Framework, e *spec.EtcdCluster) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PUT",
		fmt.Sprintf("%s/apis/coreos.com/v1/namespaces/%s/etcdclusters/%s", f.MasterHost, f.Namespace.Name, e.Name),
		bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.KubeClient.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %v", resp.Status)
	}
	return nil
}

func deleteEtcdCluster(f *framework.Framework, name string) error {
	fmt.Printf("deleting etcd cluster: %v\n", name)
	podList, err := f.KubeClient.Pods(f.Namespace.Name).List(k8sutil.EtcdPodListOpt(name))
	if err != nil {
		return err
	}
	fmt.Println("etcd pods ======")
	for i := range podList.Items {
		pod := &podList.Items[i]
		fmt.Printf("pod (%v): status (%v)\n", pod.Name, pod.Status.Phase)
		buf := bytes.NewBuffer(nil)

		if pod.Status.Phase == api.PodFailed {
			if err := getLogs(f.KubeClient, f.Namespace.Name, pod.Name, "etcd", buf); err != nil {
				return err
			}
			fmt.Println(pod.Name, "logs ===")
			fmt.Println(buf.String())
			fmt.Println(pod.Name, "logs END ===")
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := getLogs(f.KubeClient, f.Namespace.Name, "etcd-operator", "etcd-operator", buf); err != nil {
		return err
	}
	fmt.Println("etcd-operator logs ===")
	fmt.Println(buf.String())
	fmt.Println("etcd-operator logs END ===")

	req, err := http.NewRequest("DELETE",
		fmt.Sprintf("%s/apis/coreos.com/v1/namespaces/%s/etcdclusters/%s", f.MasterHost, f.Namespace.Name, name), nil)
	if err != nil {
		return err
	}
	resp, err := f.KubeClient.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %v", resp.Status)
	}
	return nil
}

func getLogs(kubecli *k8sclient.Client, ns, p, c string, out io.Writer) error {
	req := kubecli.RESTClient.Get().
		Namespace(ns).
		Resource("pods").
		Name(p).
		SubResource("log").
		Param("container", c).
		Param("tailLines", "20")

	readCloser, err := req.Stream()
	if err != nil {
		return err
	}
	defer readCloser.Close()

	_, err = io.Copy(out, readCloser)
	return err
}
