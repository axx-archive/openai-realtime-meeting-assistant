package business

import "context"

// ProviderWorkerStore connects the ordinary Worker lease/result contract to
// atomic provider-grant reservation and journal preparation. It has no provider
// client, credential, polling loop, or in-memory capability registry.
//
// The adapter must acquire an operation-bound receipt capability under its
// current lease and call CheckProviderAuthority plus source reauthorization
// immediately before external egress. The Work's frozen request predates the
// attempt; this bridge never rewrites it to add a future operation ID.
type ProviderWorkerStore struct{ *Store }

var _ WorkerStore = (*ProviderWorkerStore)(nil)

func NewProviderWorkerStore(store *Store) (*ProviderWorkerStore, error) {
	if store == nil || store.pool == nil {
		return nil, ErrInvalid
	}
	return &ProviderWorkerStore{Store: store}, nil
}

func (s *ProviderWorkerStore) PrepareOperation(ctx context.Context, scope Scope, in PrepareOperationArgs) (Attempt, error) {
	if s == nil || s.Store == nil || s.Store.pool == nil {
		return Attempt{}, ErrInvalid
	}
	token, e := NewProviderReceiptToken()
	if e != nil {
		return Attempt{}, e
	}
	prepared, e := s.Store.PrepareProviderOperation(ctx, scope, PrepareProviderOperationArgs{
		Lease: in.Lease, Operation: in.Operation, ReceiptToken: token,
	})
	// The receipt token is deliberately not retained by this bridge. Execute or
	// Reconcile acquires a fresh evidence-only token under its own current lease.
	return prepared.Attempt, e
}
