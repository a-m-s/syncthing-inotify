/* syncwatcher

   This package is a recursive wrapper for fsnotify.
   The interface is intended to be compatible with fsnotify.

   When a directory is "Watch"ed, so are all its subdirectories

   When a watched directory is moved, within, into, or out of, another watched
   directory, it is unwatched and (re)watched, as appropriate. As a special
   case, each root directory (as passed to "Watch"), is never unwatched, even
   if deleted or moved.

   WARNING: when a directory is moved there is a brief period in which other
   events inside that directory may be missed. You should assume that anything
   may have happened in that time.
*/

package main

import (
	"code.google.com/p/go.exp/fsnotify"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type SyncWatcher struct {
	Error chan error
	Event chan *fsnotify.FileEvent

	watcher   *fsnotify.Watcher
	paths     map[string]string
	roots     map[string]int
	pathMutex *sync.Mutex
}

func NewSyncWatcher() (*SyncWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	sw := &SyncWatcher{
		make(chan error),
		make(chan *fsnotify.FileEvent),
		watcher,
		make(map[string]string),
		make(map[string]int),
		&sync.Mutex{},
	}

	// Handle events from fsnotify,d eal with them,
	// and forward the interesting ones to the caller
	go func() {
		var (
			ev  *fsnotify.FileEvent
			err error
		)
		// Loop until both incoming channels are closed
		for openEvent, openErr := true, true; openEvent || openErr; {
			select {
			case ev, openEvent = <-watcher.Event:
				if openEvent {
					// Add or remove watches as appropriate
					sw.pathMutex.Lock()
					_, present := sw.paths[ev.Name]
					sw.pathMutex.Unlock()
					if present {
						// If we recognise the path then it must be a directory
						// that means its changed, and the old watches must be
						// removed.  New watches will be added when the corresponding
						// "create" event arrives.
						// This uses "removeWatch" not "RemoveWatch" on purpose
						sw.removeWatch(ev.Name)
					} else if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
						// A new, unrecognised directory was created.
						sw.watch(ev.Name)
					}

					// Forward the event to our client.
					sw.Event <- ev
				}
			case err, openErr = <-watcher.Error:
				if openErr {
					// Forward error events to our client
					sw.Error <- err
				}
			}
		}
		// If we get here then the incoming channels are closed,
		// so close the outgoing channels.
		close(sw.Event)
		close(sw.Error)
	}()

	return sw, nil
}

func (w *SyncWatcher) Close() error {
	// We close the fsnotify watcher.
	// That will close our incoming channels, and so close the SyncWatcher
	// indirectly.
	err := w.watcher.Close()
	if err != nil {
		return err
	}
	return nil
}

// This is like RemoveWatch except that it does not unwatch the root directory.
func (w *SyncWatcher) removeWatch(path string) error {
	w.pathMutex.Lock()
	defer w.pathMutex.Unlock()

	// Recursively remove all the watches from the given directory, and its
	// subdirectories. The root directory will not be unwatched (RemoveWatch
	// takes care of that).
	var recursive_remove func(dir string) error
	recursive_remove = func(dir string) error {
		children, ok := w.paths[dir]
		if ok {
			for _, child := range strings.Split(children, "\000") {
				if len(child) > 0 {
					// deliberately ignore errors from child watches
					recursive_remove(filepath.Join(dir, child))
				}
			}
			if _, isroot := w.roots[dir]; !isroot {
				delete(w.paths, dir)
				return w.watcher.RemoveWatch(dir)
			}
		}
		return errors.New("cannot remove uknown watch: " + dir)
	}

	return recursive_remove(path)
}

func (w *SyncWatcher) RemoveWatch(path string) error {
	// We want to unwatch the whole tree, including to root.
	// If we unregister the root then removeWatch will take care of the rest.
	w.pathMutex.Lock()
	if _, isroot := w.roots[path]; isroot {
		delete(w.roots, path)
	}
	w.pathMutex.Unlock()
	return w.removeWatch(path)
}

func (w *SyncWatcher) watch(path string) error {
	w.pathMutex.Lock()
	defer w.pathMutex.Unlock()

	filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			err = w.watcher.Watch(path)
			if err == nil {
				w.paths[path] = ""
				parent := filepath.Dir(path)
				if _, ok := w.paths[parent]; ok {
					// Record the directory structure so that it can be
					// walked again when we need to remove the watches.
					w.paths[parent] += filepath.Base(path) + "\000"
				}
			}
		}
		return err
	})

	return nil
}

func (w *SyncWatcher) Watch(path string) error {
	w.pathMutex.Lock()
	_, present := w.paths[path]

	if present {
		w.pathMutex.Unlock()
		return errors.New("cannot watch path twice: " + path)
	}
	w.roots[path] = 1
	w.pathMutex.Unlock()

	return w.watch(path)
}

func (w *SyncWatcher) String() string {
	w.pathMutex.Lock()
	defer w.pathMutex.Unlock()

	str := "SyncWatch:"
	for path := range w.paths {
		str += " " + path + " \"" + w.paths[path] + "\""
	}
	return str
}
