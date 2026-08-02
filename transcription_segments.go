package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	errTranscriptionSegmentInvalid         = errors.New("invalid transcription segment")
	errTranscriptionSegmentUnknown         = errors.New("unknown transcription segment")
	errTranscriptionSegmentItemConflict    = errors.New("transcription provider item conflict")
	errTranscriptionSegmentAlreadyBound    = errors.New("transcription segment already bound")
	errTranscriptionSegmentAlreadyClosed   = errors.New("transcription segment already terminal")
	errTranscriptionSegmentBindingDeferred = errors.New("transcription provider binding deferred")
	errTranscriptionSegmentChainInvalid    = errors.New("invalid transcription provider item chain")
)

// transcriptionSegmentBinding is the application-owned identity for one
// committed audio segment. Provider item IDs are transport identities only;
// every terminal event must resolve through this binding before it can consume
// attribution, metering, or persistence state.
type transcriptionSegmentBinding struct {
	SegmentID    string
	ProviderItem string
	AudioSeconds float64
}

// transcriptionSegmentBindings is deliberately connection-local. A reconnect
// discards every unbound/in-flight segment instead of letting an orphaned
// provider event consume the next connection's attribution or metering state.
// The dedicated transcription connection never creates non-audio conversation
// items. Its first input_audio_buffer.committed acknowledgement must therefore
// point at the empty conversation root; every later acknowledgement must point
// at the prior acknowledged input item. The provider can deliver those
// acknowledgements out of order, so we collect its previous_item_id chain and
// bind only a unique contiguous prefix from that root. We never infer identity
// from arrival order. Terminal events resolve by provider item ID and may
// arrive in any order.
type transcriptionSegmentBindings struct {
	mu sync.Mutex

	pending       []string
	bySegment     map[string]transcriptionSegmentBinding
	segmentByItem map[string]string
	providerPrev  map[string]string
	lastBoundItem string
	terminal      map[string]struct{}
}

func newTranscriptionSegmentBindings() *transcriptionSegmentBindings {
	return &transcriptionSegmentBindings{
		bySegment:     make(map[string]transcriptionSegmentBinding),
		segmentByItem: make(map[string]string),
		providerPrev:  make(map[string]string),
		terminal:      make(map[string]struct{}),
	}
}

func (bindings *transcriptionSegmentBindings) Commit(segmentID string, audioSeconds float64) error {
	segmentID = strings.TrimSpace(segmentID)
	if bindings == nil || segmentID == "" || audioSeconds <= 0 {
		return errTranscriptionSegmentInvalid
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	if _, exists := bindings.bySegment[segmentID]; exists {
		return fmt.Errorf("%w: %s", errTranscriptionSegmentInvalid, segmentID)
	}
	bindings.bySegment[segmentID] = transcriptionSegmentBinding{SegmentID: segmentID, AudioSeconds: audioSeconds}
	bindings.pending = append(bindings.pending, segmentID)
	return nil
}

// BindCommitted records one authoritative input_audio_buffer.committed event.
// It returns every newly resolvable application segment: an earlier missing
// acknowledgement can make a previously deferred descendant resolvable.
//
// Acknowledgement order is expressly not an identity signal. The only accepted
// first edge is previous_item_id=""; this lane starts a fresh dedicated
// conversation and emits no competing provider conversation items. Any branch,
// cycle, conflicting duplicate, terminal-before-bind, or missing predecessor
// remains fail-closed rather than consuming attribution or metering state.
func (bindings *transcriptionSegmentBindings) BindCommitted(providerItem, previousItem string) ([]transcriptionSegmentBinding, error) {
	providerItem = strings.TrimSpace(providerItem)
	previousItem = strings.TrimSpace(previousItem)
	if bindings == nil || providerItem == "" {
		return nil, errTranscriptionSegmentInvalid
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	if prior, exists := bindings.providerPrev[providerItem]; exists {
		if prior != previousItem {
			return nil, fmt.Errorf("%w: item %s previous=%s conflicts with %s", errTranscriptionSegmentItemConflict, providerItem, previousItem, prior)
		}
		if segmentID, bound := bindings.segmentByItem[providerItem]; bound {
			return []transcriptionSegmentBinding{bindings.bySegment[segmentID]}, nil
		}
		return nil, errTranscriptionSegmentBindingDeferred
	}
	if _, closed := bindings.terminal[providerItem]; closed {
		return nil, fmt.Errorf("%w: %s", errTranscriptionSegmentAlreadyClosed, providerItem)
	}
	if providerItem == previousItem {
		return nil, fmt.Errorf("%w: item %s points to itself", errTranscriptionSegmentChainInvalid, providerItem)
	}
	bindings.providerPrev[providerItem] = previousItem
	if err := bindings.validateProviderChainLocked(); err != nil {
		delete(bindings.providerPrev, providerItem)
		return nil, err
	}

	resolved, err := bindings.bindResolvablePrefixLocked()
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, errTranscriptionSegmentBindingDeferred
	}
	return resolved, nil
}

func (bindings *transcriptionSegmentBindings) validateProviderChainLocked() error {
	children := make(map[string]int, len(bindings.providerPrev))
	for item, previous := range bindings.providerPrev {
		if item == previous {
			return fmt.Errorf("%w: item %s points to itself", errTranscriptionSegmentChainInvalid, item)
		}
		children[previous]++
		if children[previous] > 1 {
			return fmt.Errorf("%w: provider chain branches from %s", errTranscriptionSegmentChainInvalid, previous)
		}
	}
	for start := range bindings.providerPrev {
		seen := make(map[string]struct{}, len(bindings.providerPrev))
		for item := start; item != ""; {
			if _, duplicate := seen[item]; duplicate {
				return fmt.Errorf("%w: cycle at %s", errTranscriptionSegmentChainInvalid, item)
			}
			seen[item] = struct{}{}
			previous, known := bindings.providerPrev[item]
			if !known {
				break
			}
			item = previous
		}
	}
	return nil
}

func (bindings *transcriptionSegmentBindings) bindResolvablePrefixLocked() ([]transcriptionSegmentBinding, error) {
	if len(bindings.pending) == 0 {
		return nil, fmt.Errorf("%w: no pending segment", errTranscriptionSegmentUnknown)
	}
	resolved := make([]transcriptionSegmentBinding, 0)
	for len(bindings.pending) > 0 {
		candidate := ""
		for item, previous := range bindings.providerPrev {
			if previous != bindings.lastBoundItem {
				continue
			}
			if _, alreadyBound := bindings.segmentByItem[item]; alreadyBound {
				continue
			}
			if candidate != "" && candidate != item {
				return nil, fmt.Errorf("%w: two unbound provider items follow %s", errTranscriptionSegmentChainInvalid, bindings.lastBoundItem)
			}
			candidate = item
		}
		if candidate == "" {
			break
		}
		if _, closed := bindings.terminal[candidate]; closed {
			return nil, fmt.Errorf("%w: %s", errTranscriptionSegmentAlreadyClosed, candidate)
		}
		segmentID := bindings.pending[0]
		record, exists := bindings.bySegment[segmentID]
		if !exists || record.ProviderItem != "" {
			return nil, fmt.Errorf("%w: %s", errTranscriptionSegmentAlreadyBound, segmentID)
		}
		record.ProviderItem = candidate
		bindings.bySegment[segmentID] = record
		bindings.segmentByItem[candidate] = segmentID
		bindings.pending = append([]string(nil), bindings.pending[1:]...)
		bindings.lastBoundItem = candidate
		resolved = append(resolved, record)
	}
	return resolved, nil
}

func (bindings *transcriptionSegmentBindings) Consume(providerItem string) (transcriptionSegmentBinding, error) {
	providerItem = strings.TrimSpace(providerItem)
	if bindings == nil || providerItem == "" {
		return transcriptionSegmentBinding{}, errTranscriptionSegmentInvalid
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	if _, closed := bindings.terminal[providerItem]; closed {
		return transcriptionSegmentBinding{}, fmt.Errorf("%w: %s", errTranscriptionSegmentAlreadyClosed, providerItem)
	}
	segmentID, exists := bindings.segmentByItem[providerItem]
	if !exists {
		// A terminal which arrives before its commit acknowledgement must not be
		// retroactively rebound to some later segment. Mark it closed and leave
		// the application segment orphaned until the connection fence resets.
		bindings.terminal[providerItem] = struct{}{}
		return transcriptionSegmentBinding{}, fmt.Errorf("%w: %s", errTranscriptionSegmentUnknown, providerItem)
	}
	record, exists := bindings.bySegment[segmentID]
	if !exists || record.ProviderItem != providerItem {
		return transcriptionSegmentBinding{}, fmt.Errorf("%w: %s", errTranscriptionSegmentItemConflict, providerItem)
	}
	delete(bindings.segmentByItem, providerItem)
	delete(bindings.bySegment, segmentID)
	bindings.terminal[providerItem] = struct{}{}
	return record, nil
}

func (bindings *transcriptionSegmentBindings) Reset() {
	if bindings == nil {
		return
	}
	bindings.mu.Lock()
	bindings.pending = nil
	bindings.bySegment = make(map[string]transcriptionSegmentBinding)
	bindings.segmentByItem = make(map[string]string)
	bindings.providerPrev = make(map[string]string)
	bindings.lastBoundItem = ""
	bindings.terminal = make(map[string]struct{})
	bindings.mu.Unlock()
}

func (bindings *transcriptionSegmentBindings) Pending() int {
	if bindings == nil {
		return 0
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	return len(bindings.bySegment)
}
