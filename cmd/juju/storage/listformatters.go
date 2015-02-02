package storage

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/dustin/go-humanize"
	"github.com/juju/errors"
)

// formatListTabular returns a tabular summary of storage instances.
func formatListTabular(value interface{}) ([]byte, error) {
	storageInfo, ok := value.(map[string]map[string]StorageInfo)
	if !ok {
		return nil, errors.Errorf("expected value of type %T, got %T", storageInfo, value)
	}
	var out bytes.Buffer
	// To format things into columns.
	tw := tabwriter.NewWriter(&out, 0, 1, 1, ' ', 0)
	p := func(values ...interface{}) {
		for _, v := range values {
			fmt.Fprintf(tw, "%v\t", v)
		}
		fmt.Fprintln(tw)
	}
	p("[Storage]")
	p("OWNER\tID\tSIZE\tLOCATION")

	// First sort by owners
	owners := make([]string, 0, len(storageInfo))
	for order := range storageInfo {
		owners = append(owners, order)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		all := storageInfo[owner]

		// Then sort by storage ids
		storageIds := make([]string, 0, len(all))
		for anId := range all {
			storageIds = append(storageIds, anId)
		}
		sort.Strings(byStorageId(storageIds))

		for _, storageId := range storageIds {
			// TODO we should be listing attachments here,
			// not storage instances. This needs to change
			// when the model does. For now we are assume
			// all owners are units (which is currently the
			// case.)
			info := all[storageId]
			location := "-"
			if info.Location != nil {
				location = *info.Location
			}
			totalSize := "(unknown)"
			if info.TotalSize != nil {
				totalSize = humanize.IBytes(*info.TotalSize * humanize.MiByte)
			}
			p(owner, storageId, totalSize, location)
		}
	}
	tw.Flush()

	return out.Bytes(), nil
}

type byStorageId []string

func (s byStorageId) Len() int {
	return len(s)
}

func (s byStorageId) Swap(a, b int) {
	s[a], s[b] = s[b], s[a]
}

func (s byStorageId) Less(a, b int) bool {
	apos := strings.LastIndex(s[a], "/")
	bpos := strings.LastIndex(s[b], "/")
	if apos == -1 || bpos == -1 {
		panic("invalid storage ID")
	}
	aname := s[a][:apos]
	bname := s[b][:bpos]
	if aname == bname {
		aid, err := strconv.Atoi(s[a][apos+1:])
		if err != nil {
			panic(err)
		}
		bid, err := strconv.Atoi(s[b][bpos+1:])
		if err != nil {
			panic(err)
		}
		return aid < bid
	}
	return aname < bname
}
