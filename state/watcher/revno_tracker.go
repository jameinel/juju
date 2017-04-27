package watcher

import (
	"strings"
)

type modelRevnos struct {
	uuid   string
	revnos map[string]int64
}

type collectionRevnos struct {
	name            string
	opaque          map[interface{}]int64
	modelUUIDRevnos map[string]*modelRevnos
}

// lastRevnoTracker keeps track of all the revnos that we see in the database.
// It is slightly smarter than a simple map, because it collects ids that are
// in the same modeluuid into an aggregated map, so that we don't store the
// modeluuid in memory 1000s of times. It also aggregates the collection names
// in a similar fashion.
// Note: we *might* want to use a string cache to remove duplicate keys between
// instances as well.
type lastRevnoTracker struct {
	byCollection map[string]*collectionRevnos
}

func (t *lastRevnoTracker) findAsOpaque(collection string, id interface{}) (int64, bool) {
	coll := t.byCollection[collection]
	if coll == nil {
		return -1, false
	}
	if revno, found := coll.opaque[id]; found {
		return revno, true
	}
	return -1, false
}

func (t *lastRevnoTracker) findAsModelStr(collection, modelUUID, id string) (int64, bool) {
	coll := t.byCollection[collection]
	if coll == nil {
		return -1, false
	}
	modelRevnos := coll.modelUUIDRevnos[modelUUID]
	if modelRevnos == nil {
		return -1, false
	}
	if revno, found := modelRevnos.revnos[id]; found {
		return revno, true
	}
	return -1, false
}

// asModelUUIDId tries to take an opaque "id" and see if it is actually a string
// of the form "model-uuid:id". It requires that both model-uuid and id are at
// least 1 byte long. If it does not fit the pattern, then modelUUID will be
// returned as an empty string, which can be used to determine that we actually
// want to treat the value as an opaque interface{}.
// If it does fit the pattern then it returns (modelUUID, id) as separate values.
func asModelUUIDId(id interface{}) (string, string) {
	idStr, ok := id.(string)
	if !ok {
		return "", ""
	}
	colon := strings.Index(idStr, ":")
	if colon <= 0 || colon == len(idStr)-1 {
		return "", ""
	}
	return idStr[:colon], idStr[colon+1:]
}

// Find the revno of the given key.
// This returns both the cached revno, and whether the revno was actually found
// in the cache. So you can distinguish -1 from "key never seen" from "key
// explicitly deleted and thus set to -1".
func (t *lastRevnoTracker) Find(collection string, id interface{}) (int64, bool) {
	modelUUID, idTail := asModelUUIDId(id)
	if modelUUID != "" {
		return t.findAsModelStr(collection, modelUUID, idTail)
	}
	return t.findAsOpaque(collection, id)
}

func (t *lastRevnoTracker) ensureCollection(collection string) *collectionRevnos {
	coll := t.byCollection[collection]
	if coll != nil {
		return coll
	}
	// Create a new set for this collection
	coll = &collectionRevnos{
		name:            collection,
		// We late allocate opaque or modelUUIDRevnos. This is because for
		// a given collection, we are unlikely to ever need both, as either
		// all keys are modelUUID prefixed or none are.
		// opaque:          make(map[interface{}]int64),
		// modelUUIDRevnos: make(map[string]*modelRevnos),
	}
	t.byCollection[collection] = coll
	return coll
}

func (t *lastRevnoTracker) updateAsOpaque(collection string, id interface{}, revno int64) bool {
	coll := t.ensureCollection(collection)
	lastRevno, found := coll.opaque[id]
	if !found {
		lastRevno = -1
	}
	if coll.opaque == nil {
		coll.opaque = make(map[interface{}]int64)
	}
	coll.opaque[id] = revno
	return lastRevno != revno
}

func (t *lastRevnoTracker) ensureModel(collection, modelUUID string) *modelRevnos {
	coll := t.ensureCollection(collection)
	model := coll.modelUUIDRevnos[modelUUID]
	if model != nil {
		return model
	}
	model = &modelRevnos{
		uuid:   modelUUID,
		revnos: make(map[string]int64),
	}
	if coll.modelUUIDRevnos == nil {
		coll.modelUUIDRevnos = make(map[string]*modelRevnos)
	}
	coll.modelUUIDRevnos[modelUUID] = model
	return model
}

func (t *lastRevnoTracker) updateAsModelString(collection, modelUUID, id string, revno int64) bool {
	model := t.ensureModel(collection, modelUUID)
	lastRevno, found := model.revnos[id]
	if !found {
		lastRevno = -1
	}
	model.revnos[id] = revno
	return lastRevno != revno
}

// Update the tracked revno to be revno, and return true if the value changed.
func (t *lastRevnoTracker) Update(collection string, id interface{}, revno int64) bool {
	modelUUID, idTail := asModelUUIDId(id)
	if modelUUID != "" {
		return t.updateAsModelString(collection, modelUUID, idTail, revno)
	}
	return t.updateAsOpaque(collection, id, revno)
}

func NewRevnoTracker() *lastRevnoTracker {
	return &lastRevnoTracker{
		byCollection: make(map[string]*collectionRevnos),
	}
}
