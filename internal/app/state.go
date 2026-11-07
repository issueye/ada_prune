package app

import (
	"github.com/learningdog1/ada_prune/internal/store"
)

// stateStore adapts the store's provider_state table to gateway.StateStore.
//
// Adapters never see the store: they get this narrow interface, which keeps
// vendor state out of the main tables and the adapter packages out of the
// persistence layer.
type stateStore struct {
	st *store.Store
}

// Get loads one adapter-private value.
func (s stateStore) Get(providerID int64, key string, v any) (bool, error) {
	return s.st.GetProviderState(providerID, key, v)
}

// Set stores one adapter-private value.
func (s stateStore) Set(providerID int64, key string, v any) error {
	return s.st.SetProviderState(providerID, key, v)
}

// Delete removes one adapter-private value.
func (s stateStore) Delete(providerID int64, key string) error {
	return s.st.DeleteProviderState(providerID, key)
}
