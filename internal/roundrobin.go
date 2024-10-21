package internal

import (
	"errors"
	"sync/atomic"
)

type roundRobin struct {
	endpointIndex atomic.Uint32
}

func (roundRobin *roundRobin) balanced(endpoints []*Endpoint, retriedIndexesF any) (*Endpoint, error) {
	if retriedIndexesF == nil {
		retriedIndexesF = []uint32{}
	}
	retriedIndexes := retriedIndexesF.([]uint32)

	oldIndex := roundRobin.endpointIndex.Load()
	choosenEndp := endpoints[oldIndex]
	endpointsLen := len(endpoints)
	//if reached enpoints length restart from 0
	var newIndex uint32 = 0
	if oldIndex < uint32(endpointsLen)-1 {
		newIndex = oldIndex + 1
	}

	if choosenEndp.Alive.Load() {

		if roundRobin.endpointIndex.CompareAndSwap(oldIndex, newIndex) {
			return endpoints[oldIndex], nil
		}

		return roundRobin.balanced(endpoints, nil)
	}

	//trying to swap index if it is not already swapped, this index is not alive
	roundRobin.endpointIndex.CompareAndSwap(oldIndex, newIndex)

	if len(retriedIndexes) == endpointsLen {
		return nil, errors.New("all endpoints down")
	}

	found := false
	for _, retryIndex := range retriedIndexes {
		if retryIndex == oldIndex {
			found = true
			break
		}
	}

	if !found {
		retriedIndexes = append(retriedIndexes, oldIndex)
	}

	return roundRobin.balanced(endpoints, retriedIndexes)
}
