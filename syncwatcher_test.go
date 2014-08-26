// syncwatcher_test.go
package main

import (
	"code.google.com/p/go.exp/fsnotify"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var tmpdir string
var watchdir string

func init() {
	tmpdir = os.TempDir()
	watchdir = filepath.Join(tmpdir, fmt.Sprintf("watchdir.%d", os.Getpid()))
}

func newSW(t *testing.T) (sw *SyncWatcher) {
	sw, err := NewSyncWatcher()
	if sw == nil || err != nil {
		t.Error("NewSyncWatcher failed:", err)
	}
	return
}

func mkdir(t *testing.T, path string) {
	err := os.Mkdir(path, 0700)
	if err != nil {
		t.Error("Cannot create test directory:", err)
	}
	return
}

func watch(t *testing.T, sw *SyncWatcher, path string) {
	err := sw.Watch(path)
	if err != nil {
		t.Error("Watch failed:", err)
	}
	return
}

func createEmptyFile(t *testing.T, path string) {
	fd, err := os.Create(path)
	if err != nil {
		t.Error("Could not touch file:", err)
		return
	}
	fd.Close()
	return
}

func removeAll(t *testing.T, path string) {
	err := os.RemoveAll(path)
	if err != nil {
		t.Error("Could not delete directory tree:", err)
	}
	return
}

func expectEvent(t *testing.T, sw *SyncWatcher) (ev *fsnotify.FileEvent, ok bool) {
	timeout := time.After(time.Second * 2)
	select {
	case ev, ok = <-sw.Event:
		if ok {
			t.Log("Event:", ev)
		} else {
			t.Log("Event: channel closed")
		}
	case err, eok := <-sw.Error:
		t.Error("Unexpected error from SyncWatcher channel:", err, eok)
	case _ = <-timeout:
		t.Error("no response")
	}
	return
}

func expectClosed(t *testing.T, sw *SyncWatcher) {
	timeout := time.After(time.Second * 2)
	var ev *fsnotify.FileEvent
	var err error
Loop:
	for ok, eok := true, true; ok || eok; {
		select {
		case ev, ok = <-sw.Event:
			if ok {
				t.Error("Unexpected event:", ev)
			}
		case err, eok = <-sw.Error:
			if eok {
				t.Error("Unexpected error from SyncWatcher channel:", err)
			}
		case _ = <-timeout:
			t.Error("Channels did not close within time limit")
			break Loop
		}
	}
	return
}

func TestWatchFiles(t *testing.T) {
	mkdir(t, watchdir)
	defer removeAll(t, watchdir)

	sw := newSW(t)
	watch(t, sw, watchdir)

	file1 := filepath.Join(watchdir, "a")
	file2 := filepath.Join(watchdir, "b")

	// Test: File creation
	createEmptyFile(t, file1)
	ev, ok := expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != file1 {
		t.Error("Expected file create event")
	}

	// Test: File rename
	os.Rename(file1, file2)
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsRename() || ev.Name != file1 {
		t.Error("Expected file rename event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != file2 {
		t.Error("Expected file create event")
	}

	// Test: File modification
	fd, err := os.OpenFile(file2, os.O_WRONLY, 0600)
	if err != nil {
		t.Error("Could not open file:", err)
	}
	fmt.Fprintln(fd, "blah blah blah")
	fd.Close()
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsModify() || ev.Name != file2 {
		t.Error("Expected file modify event")
	}

	// Test: File deletion
	os.Remove(file2)
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsDelete() || ev.Name != file2 {
		t.Error("Expected file delete event")
	}

	sw.Close()
	expectClosed(t, sw)
}

func TestRecursiveWatch(t *testing.T) {
	mkdir(t, watchdir)
	defer removeAll(t, watchdir)

	sw := newSW(t)
	watch(t, sw, watchdir)

	// Test: check internal state
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: ""}) {
		t.Error("sw.paths does not have expected contents")
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	dir1 := filepath.Join(watchdir, "a")
	dir2 := filepath.Join(watchdir, "b")

	// Test: Directory creation
	mkdir(t, dir1)
	ev, ok := expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != dir1 {
		t.Error("Expected directory create event")
	}

	// Test: check internal state
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: filepath.Base(dir1) + "\000", dir1: ""}) {
		t.Error("sw.paths does not have expected contents")
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	// Test: Directory modification
	file1 := filepath.Join(dir1, "c")
	createEmptyFile(t, file1)
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != file1 {
		t.Error("Expected file create event")
	}

	// Test: Directory rename
	os.Rename(dir1, dir2)
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsRename() || ev.Name != dir1 {
		t.Error("Expected directory rename event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != dir2 {
		t.Error("Expected directory create event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsRename() || ev.Name != dir1 {
		t.Error("Expected directory rename event")
	}

	// Test: check internal state
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: filepath.Base(dir1) + "\000" + filepath.Base(dir2) + "\000", dir2: ""}) {
		t.Error("sw.paths does not have expected contents:", sw)
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	// fix up the location of now-moved file1
	file1 = filepath.Join(dir2, filepath.Base(file1))

	// Test: Directory modification
	file2 := filepath.Join(dir2, "d")
	createEmptyFile(t, file2)
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != file2 {
		t.Error("Expected file create event")
	}

	// Test: Directory deletion
	removeAll(t, dir2)
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsDelete() || (ev.Name != file1 && ev.Name != file2) {
		t.Error("Expected file delete event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsDelete() || (ev.Name != file1 && ev.Name != file2) {
		t.Error("Expected directory delete event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsDelete() || ev.Name != dir2 {
		t.Error("Expected directory delete event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsDelete() || ev.Name != dir2 {
		t.Error("Expected directory delete event")
	}

	// Test: check internal state
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: filepath.Base(dir1) + "\000" + filepath.Base(dir2) + "\000"}) {
		t.Error("sw.paths does not have expected contents:", sw)
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	sw.Close()
	expectClosed(t, sw)
}

func TestMoveIn(t *testing.T) {
	mkdir(t, watchdir)
	defer removeAll(t, watchdir)

	sw := newSW(t)
	watch(t, sw, watchdir)

	// Test: check internal state
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: ""}) {
		t.Error("sw.paths does not have expected contents")
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	createdir := filepath.Join(tmpdir, fmt.Sprintf("newdir.%d", os.Getpid()))
	moveddir := filepath.Join(watchdir, "newdir")

	// Create a directory outside the watch directory.
	// Give it a sub directory
	mkdir(t, createdir)
	mkdir(t, filepath.Join(createdir, "subdir"))

	// Test: check internal state
	// Nothing should have changed
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: ""}) {
		t.Error("sw.paths does not have expected contents")
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	// Test: Move external directory in
	os.Rename(createdir, moveddir)
	ev, ok := expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != moveddir {
		t.Error("Expected directory create event")
	}

	// Test: check internal state
	// Two new directories should have been added
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: filepath.Base(moveddir) + "\000", moveddir: "subdir\000", filepath.Join(moveddir, "subdir"): ""}) {
		t.Error("sw.paths does not have expected contents:", sw)
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	sw.Close()
	expectClosed(t, sw)
}

func TestMoveOut(t *testing.T) {
	mkdir(t, watchdir)
	defer removeAll(t, watchdir)

	sw := newSW(t)
	watch(t, sw, watchdir)

	// Test: check internal state
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: ""}) {
		t.Error("sw.paths does not have expected contents")
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	createdir := filepath.Join(watchdir, "newdir")
	moveddir := filepath.Join(tmpdir, fmt.Sprintf("newdir.%d", os.Getpid()))
	defer os.RemoveAll(moveddir)

	// Create a directory outside the watch directory.
	// Give it a sub directory
	mkdir(t, createdir)
	mkdir(t, filepath.Join(createdir, "subdir"))
	ev, ok := expectEvent(t, sw)
	if !ok || !ev.IsCreate() || ev.Name != createdir {
		t.Error("Expected directory create event")
	}

	// Test: check internal state
	// Two new directories should have been added
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: filepath.Base(createdir) + "\000", createdir: "subdir\000", filepath.Join(createdir, "subdir"): ""}) {
		t.Error("sw.paths does not have expected contents:", sw)
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	// Test: Move directory out of the watched area
	os.Rename(createdir, moveddir)
	ev, ok = expectEvent(t, sw)
	if ok && ev.IsCreate() && ev.Name == filepath.Join(createdir, "subdir") {
		// There's a race condition in the previous test
		// This create event is not required, but OK, so skip it
		ev, ok = expectEvent(t, sw)
	}
	if !ok || !ev.IsRename() || ev.Name != createdir {
		t.Error("Expected directory rename event")
	}
	ev, ok = expectEvent(t, sw)
	if !ok || !ev.IsRename() || ev.Name != createdir {
		t.Error("Expected directory rename event")
	}

	// Test: check internal state
	// The directories should have been removed
	if !reflect.DeepEqual(sw.paths, map[string]string{watchdir: filepath.Base(createdir) + "\000"}) {
		t.Error("sw.paths does not have expected contents")
	}
	if !reflect.DeepEqual(sw.roots, map[string]int{watchdir: 1}) {
		t.Error("sw.roots does not have expected contents")
	}

	sw.Close()
	expectClosed(t, sw)
}
