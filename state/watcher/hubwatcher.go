// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package watcher

import (
	"fmt"

	"github.com/juju/errors"
	"gopkg.in/juju/worker.v1"
	"gopkg.in/tomb.v2"
)

// HubSource represents the listening aspects of the pubsub hub.
type HubSource interface {
	SubscribeMatch(matcher func(string) bool, handler func(string, interface{})) func()
}

// filterFactor represents the portion of a first-order filter that is calculating a running average.
// This is the weight of the new inputs. A value of 0.5 would represent averaging over just 2 samples,
// 0.1 would be 10 samples, 0.01 corresponds to averaging over approximately 100 items.
const filterFactor = 0.01

// HubWatcher listens to events from the hub and passes them on to the registered
// watchers.
type HubWatcher struct {
	hub    HubSource
	logger Logger

	tomb tomb.Tomb

	// watches holds the observers managed by Watch/Unwatch.
	watches map[watchKey][]watchInfo

	// syncEvents and requestEvents contain the events to be
	// dispatched to the watcher channels. They're queued during
	// processing and flushed at the end to simplify the algorithm.
	// The two queues are separated because events from sync are
	// handled in reverse order due to the way the algorithm works.
	syncEvents, requestEvents []event

	// request is used to deliver requests from the public API into
	// the the goroutine loop.
	request chan interface{}

	// changes are events published to the hub.
	changes chan Change

	// lastSyncLen was the length of syncEvents in the last flush()
	lastSyncLen int

	// maxSyncLen is the longest we've seen syncEvents
	maxSyncLen int

	// averageSyncLen applies a first-order filter for every time we flush(), to give us an idea of how large,
	// on average, our sync queue is
	averageSyncLen float64

	// syncEventDocCount is all sync events that we've ever processed for individual docs
	syncEventDocCount uint64

	// syncEventCollectionCount is all the sync events we've processed for collection watches
	syncEventCollectionCount uint64

	// requestCount is all requests that we've ever processed
	requestCount uint64

	// changeCount is the number of change events we've processed
	changeCount uint64

	// revnoMapBytes tracks how big our revnomap is in approximate bytes
	revnoMapBytes uintptr
}

// NewHubWatcher returns a new watcher observing Change events published to the
// hub.
func NewHubWatcher(hub HubSource, logger Logger) *HubWatcher {
	watcher, _ := newHubWatcher(hub, logger)
	return watcher
}

func newHubWatcher(hub HubSource, logger Logger) (*HubWatcher, <-chan struct{}) {
	if logger == nil {
		logger = noOpLogger{}
	}
	started := make(chan struct{})
	w := &HubWatcher{
		hub:     hub,
		logger:  logger,
		watches: make(map[watchKey][]watchInfo),
		request: make(chan interface{}),
		changes: make(chan Change),
	}
	w.tomb.Go(func() error {
		unsub := hub.SubscribeMatch(
			func(string) bool { return true }, w.receiveEvent,
		)
		defer unsub()
		close(started)
		err := w.loop()
		cause := errors.Cause(err)
		// tomb expects ErrDying or ErrStillAlive as
		// exact values, so we need to log and unwrap
		// the error first.
		if err != nil && cause != tomb.ErrDying {
			logger.Infof("watcher loop failed: %v", err)
		}
		return cause
	})
	return w, started
}

func (w *HubWatcher) receiveEvent(topic string, data interface{}) {
	switch topic {
	case txnWatcherStarting:
		// This message is published when the main txns.log watcher starts. If
		// this message is received here it means that the main watcher has
		// restarted. It is highly likely that it restarted because it lost
		// track of where it was, or the connection shut down. Either way, we
		// need to stop this worker to release all the watchers.
		w.tomb.Kill(errors.New("txn watcher restarted"))
	case txnWatcherCollection:
		change, ok := data.(Change)
		if !ok {
			w.logger.Warningf("incoming event not a Change")
			return
		}
		select {
		case w.changes <- change:
		case <-w.tomb.Dying():
		}
	default:
		w.logger.Warningf("programming error, unknown topic: %q", topic)
	}
}

// Kill is part of the worker.Worker interface.
func (w *HubWatcher) Kill() {
	w.tomb.Kill(nil)
}

// Wait is part of the worker.Worker interface.
func (w *HubWatcher) Wait() error {
	return w.tomb.Wait()
}

// Stop stops all the watcher activities.
func (w *HubWatcher) Stop() error {
	return worker.Stop(w)
}

// Dead returns a channel that is closed when the watcher has stopped.
func (w *HubWatcher) Dead() <-chan struct{} {
	return w.tomb.Dead()
}

// Err returns the error with which the watcher stopped.
// It returns nil if the watcher stopped cleanly, tomb.ErrStillAlive
// if the watcher is still running properly, or the respective error
// if the watcher is terminating or has terminated with an error.
func (w *HubWatcher) Err() error {
	return w.tomb.Err()
}

func (w *HubWatcher) sendReq(req interface{}) {
	select {
	case w.request <- req:
	case <-w.tomb.Dying():
	}
}

// Watch starts watching the given collection and document id.
// An event will be sent onto ch whenever a matching document's txn-revno
// field is observed to change after a transaction is applied. The revno
// parameter holds the currently known revision number for the document.
// Non-existent documents are represented by a -1 revno.
func (w *HubWatcher) Watch(collection string, id interface{}, ch chan<- Change) {
	if id == nil {
		panic("watcher: cannot watch a document with nil id")
	}
	w.sendReq(reqWatch{watchKey{collection, id}, watchInfo{ch: ch, filter: nil}})
}

// WatchCollection starts watching the given collection.
// An event will be sent onto ch whenever the txn-revno field is observed
// to change after a transaction is applied for any document in the collection.
func (w *HubWatcher) WatchCollection(collection string, ch chan<- Change) {
	w.WatchCollectionWithFilter(collection, ch, nil)
}

// WatchCollectionWithFilter starts watching the given collection.
// An event will be sent onto ch whenever the txn-revno field is observed
// to change after a transaction is applied for any document in the collection, so long as the
// specified filter function returns true when called with the document id value.
func (w *HubWatcher) WatchCollectionWithFilter(collection string, ch chan<- Change, filter func(interface{}) bool) {
	w.sendReq(reqWatch{watchKey{collection, nil}, watchInfo{ch: ch, filter: filter}})
}

// Unwatch stops watching the given collection and document id via ch.
func (w *HubWatcher) Unwatch(collection string, id interface{}, ch chan<- Change) {
	if id == nil {
		panic("watcher: cannot unwatch a document with nil id")
	}
	w.sendReq(reqUnwatch{watchKey{collection, id}, ch})
}

// UnwatchCollection stops watching the given collection via ch.
func (w *HubWatcher) UnwatchCollection(collection string, ch chan<- Change) {
	w.sendReq(reqUnwatch{watchKey{collection, nil}, ch})
}

// HubWatcherStats defines a few metrics that the hub watcher tracks
type HubWatcherStats struct {
	// WatchKeyCount is the number of keys being watched
	WatchKeyCount int
	// WatchCount is the number of watchers (keys can be watched by multiples)
	WatchCount uint64
	// SyncQueueCap is the maximum buffer size for synchronization events
	SyncQueueCap int
	// SyncQueueLen is the current number of events being queued
	SyncQueueLen int
	// SyncLastLen was the length of SyncQueue the last time we flushed
	SyncLastLen int
	// SyncAvgLen is a smoothed average of recent sync lengths
	SyncAvgLen int
	// SyncMaxLen was the longest we've seen SyncQueue when flushing
	SyncMaxLen int
	// SyncEventDocCount is the number of sync events we've generated for specific documents
	SyncEventDocCount uint64
	// SyncEventCollCount is the number of sync events we've generated for documents changed in collections
	SyncEventCollCount uint64
	// RequestEventCount is the number of request events we've generated
	// (documents being watched that changed since the request came in)
	RequestEventCount uint64
	// RequestQueueCap is the maximum size of the request queue buffer
	RequestQueueCap int
	// RequestQueueLen is the current number of requested events
	RequestQueueLen int
	// RequestCount is the number of requests (reqWatch/reqUnwatch, etc) that we've seen
	RequestCount uint64
	// ChangeCount is the number of changes we've processed
	ChangeCount uint64
}

type reqStats struct {
	ch chan<- HubWatcherStats
}

func (w *HubWatcher) Stats() HubWatcherStats {
	ch := make(chan HubWatcherStats)
	w.sendReq(reqStats{ch: ch})
	select {
	case <-w.tomb.Dying():
		return HubWatcherStats{}
	case stats := <-ch:
		return stats
	}
}

// Report conforms to the worker.Runner.Report interface for returning information about the active worker.
func (w *HubWatcher) Report() map[string]interface{} {
	stats := w.Stats()
	return map[string]interface{}{
		"watch-count":           stats.WatchCount,
		"watch-key-count":       stats.WatchKeyCount,
		"sync-queue-cap":        stats.SyncQueueCap,
		"sync-queue-len":        stats.SyncQueueLen,
		"sync-last-len":         stats.SyncLastLen,
		"sync-avg-len":          stats.SyncAvgLen,
		"sync-max-len":          stats.SyncMaxLen,
		"sync-event-doc-count":  stats.SyncEventDocCount,
		"sync-event-coll-count": stats.SyncEventCollCount,
		"request-queue-cap":     stats.RequestQueueCap,
		"request-queue-len":     stats.RequestQueueLen,
		"request-event-count":   stats.RequestEventCount,
		"request-count":         stats.RequestCount,
		"change-count":          stats.ChangeCount,
	}
}

// loop implements the main watcher loop.
// period is the delay between each sync.
func (w *HubWatcher) loop() error {
	for {
		select {
		case <-w.tomb.Dying():
			return errors.Trace(tomb.ErrDying)
		case change := <-w.changes:
			w.queueChange(change)
		case req := <-w.request:
			w.handle(req)
		}
		for (len(w.syncEvents) + len(w.requestEvents)) > 0 {
			select {
			case <-w.tomb.Dying():
				return errors.Trace(tomb.ErrDying)
			default:
				w.flush()
			}
		}
	}
}

func (w *HubWatcher) flush() {
	// syncEvents are stored first in first out.
	// syncEvents may grow during the looping here if new
	// watch events come in while we are notifying other watchers.
	for i := 0; i < len(w.syncEvents); i++ {
		// We need to reget the address value each time through the loop
		// as the slice may be reallocated.
		for e := &w.syncEvents[i]; e.ch != nil; e = &w.syncEvents[i] {
			w.logger.Tracef("syncEvents: e.ch=%v len(%d), cap(%d)", e.ch, len(w.syncEvents), cap(w.syncEvents))
			select {
			case <-w.tomb.Dying():
				return
			case req := <-w.request:
				w.handle(req)
				continue
			case change := <-w.changes:
				w.queueChange(change)
				continue
			case e.ch <- Change{C: e.key.c, Id: e.key.id, IsDeleted: e.isDeleted}:
				w.logger.Tracef("e.ch=%v has been notified", e.ch)
			}
			break
		}
	}
	w.lastSyncLen = len(w.syncEvents)
	if w.lastSyncLen > w.maxSyncLen {
		w.maxSyncLen = w.lastSyncLen
	}
	// first-order filter: https://en.wikipedia.org/wiki/Low-pass_filter#Discrete-time_realization
	// This allows us to compute an "average" without having to actually track N samples.
	w.averageSyncLen = (filterFactor * float64(w.lastSyncLen)) + ((1.0 - filterFactor) * w.averageSyncLen)
	w.syncEvents = w.syncEvents[:0]
	// TODO(jam): 2018-11-07 This would probably be a good time to wipe syncEvents if cap(syncEvents) is significantly
	// larger than averageSyncLen. Consider something like "if cap(syncEventsLen) > 10*w.averageSyncLen".
	// That means that we can shrink the buffer after an outlier, rather than requiring it to always be the longest
	// it was ever needed.
	w.logger.Tracef("syncEvents: len(%d), cap(%d) avg(%.1f)", len(w.syncEvents), cap(w.syncEvents), w.averageSyncLen)

	// requestEvents are stored oldest first, and
	// may grow during the loop.
	for i := 0; i < len(w.requestEvents); i++ {
		// We need to reget the address value each time through the loop
		// as the slice may be reallocated.
		for e := &w.requestEvents[i]; e.ch != nil; e = &w.requestEvents[i] {
			select {
			case <-w.tomb.Dying():
				return
			case req := <-w.request:
				w.handle(req)
				continue
			case change := <-w.changes:
				w.queueChange(change)
				continue
			case e.ch <- Change{C: e.key.c, Id: e.key.id, IsDeleted: e.isDeleted}:
			}
			break
		}
	}
	w.requestEvents = w.requestEvents[:0]
}

// handle deals with requests delivered by the public API
// onto the background watcher goroutine.
func (w *HubWatcher) handle(req interface{}) {
	w.logger.Tracef("got request: %#v", req)
	w.requestCount++
	switch r := req.(type) {
	case reqWatch:
		for _, info := range w.watches[r.key] {
			if info.ch == r.info.ch {
				panic(fmt.Errorf("tried to re-add channel %v for %s", info.ch, r.key))
			}
		}
		// TODO(jam): 2018-11-15 if the document is already deleted, will we never send an 'its deleted' event that
		// the callers can handle?
		// Shouldn't they already handle the "I got a change, and then it was deleted" thus the event we send them is
		// that they go lookup the document, and find that 'no, it really is deleted now', and they handle it already?
		w.requestEvents = append(w.requestEvents, event{ch: r.info.ch, key: r.key, isDeleted: false})
		w.watches[r.key] = append(w.watches[r.key], r.info)
	case reqUnwatch:
		watches := w.watches[r.key]
		removed := false
		for i, info := range watches {
			if info.ch == r.ch {
				watches[i] = watches[len(watches)-1]
				w.watches[r.key] = watches[:len(watches)-1]
				// TODO(jam): 2018-11-15 if this is the last watch, remove w.watches[r.key] entirely
				removed = true
				break
			}
		}
		if !removed {
			panic(fmt.Errorf("tried to remove missing channel %v for %s", r.ch, r.key))
		}
		for i := range w.requestEvents {
			e := &w.requestEvents[i]
			if r.key.match(e.key) && e.ch == r.ch {
				e.ch = nil
			}
		}
		for i := range w.syncEvents {
			e := &w.syncEvents[i]
			if r.key.match(e.key) && e.ch == r.ch {
				e.ch = nil
			}
		}
	case reqStats:
		var watchCount uint64
		for _, watches := range w.watches {
			watchCount += uint64(len(watches))
		}
		stats := HubWatcherStats{
			ChangeCount:        w.changeCount,
			WatchKeyCount:      len(w.watches),
			WatchCount:         watchCount,
			SyncQueueCap:       cap(w.syncEvents),
			SyncQueueLen:       len(w.syncEvents),
			SyncLastLen:        w.lastSyncLen,
			SyncMaxLen:         w.maxSyncLen,
			SyncAvgLen:         int(w.averageSyncLen + 0.5),
			SyncEventCollCount: w.syncEventCollectionCount,
			SyncEventDocCount:  w.syncEventDocCount,
			RequestQueueCap:    cap(w.requestEvents),
			RequestQueueLen:    len(w.requestEvents),
			RequestCount:       w.requestCount,
		}
		select {
		case <-w.tomb.Dying():
			return
		case r.ch <- stats:
		}
	default:
		panic(fmt.Errorf("unknown request: %T", req))
	}
}

// queueChange queues up the change for the registered watchers.
func (w *HubWatcher) queueChange(change Change) {
	w.changeCount++
	w.logger.Tracef("got change document: %#v", change)
	key := watchKey{change.C, change.Id}

	// Queue notifications for per-collection watches.
	for _, info := range w.watches[watchKey{change.C, nil}] {
		if info.filter != nil && !info.filter(change.Id) {
			continue
		}
		w.syncEvents = append(w.syncEvents, event{ch: info.ch, key: key, isDeleted: change.IsDeleted})
		w.syncEventCollectionCount++
	}

	// Queue notifications for per-document watches.
	infos := w.watches[key]
	for _, info := range infos {
		w.syncEvents = append(w.syncEvents, event{ch: info.ch, key: key, isDeleted: change.IsDeleted})
		w.syncEventDocCount++
	}
}
