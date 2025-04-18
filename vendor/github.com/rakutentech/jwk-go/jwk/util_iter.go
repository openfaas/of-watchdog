package jwk

import (
	"iter"
	"slices"
)

// All returns a sequence of all KeySpecs in the KeySpecSet.
func (ks KeySpecSet) All() iter.Seq[KeySpec] {
	return slices.Values(ks.Keys)
}

// FilterIter filters the specified KeySpecSet with a filter function and
// returns an iter.Seq.
//
// filter is a predicate that accepts a KeySpec and returns a boolean value.
func (ks KeySpecSet) FilterIter(filter func(key *KeySpec) bool) iter.Seq[KeySpec] {
	return func(yield func(KeySpec) bool) {
		for _, key := range ks.Keys {
			if filter(&key) {
				if !yield(key) {
					return
				}
			}
		}
	}
}

// OnlyKeyTypesIter filters the KeySpecSet and returns only KeySpecs which match
// the specified key type and curve.
//
// See KeySpecSet.OnlyKeyTypes for more information.
func (ks KeySpecSet) OnlyKeyTypesIter(keyTypes ...string) iter.Seq[KeySpec] {
	return func(yield func(KeySpec) bool) {
		for _, key := range ks.Keys {
			for _, allowedKty := range keyTypes {
				if key.IsKeyType(allowedKty) {
					if !yield(key) {
						return
					}
					break
				}
			}
		}
	}
}

// CollectKeySet collects all KeySpecs in the sequence and returns a new KeySpecSet.
func CollectKeySet(seq iter.Seq[KeySpec]) KeySpecSet {
	var keys []KeySpec
	seq(func(key KeySpec) bool {
		keys = append(keys, key)
		return true
	})
	return KeySpecSet{Keys: keys}
}
