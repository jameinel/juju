package watcher

func TrackerFindOpaque(tracker *lastRevnoTracker, collection string, id interface{}) int64 {
	val, _ := tracker.findAsOpaque(collection, id)
	return val
}

func TrackerFindModelId(tracker *lastRevnoTracker, collection, modelUUID, id string) int64 {
	val, _ := tracker.findAsModelStr(collection, modelUUID, id)
	return val
}
