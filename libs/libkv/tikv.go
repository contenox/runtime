package libkv

// type TikvManager struct {
// 	client *txnkv.Client
// }

// func NewTiKVManager(pdEndpoints []string) (*TikvManager, error) {
// 	client, err := txnkv.NewClient(pdEndpoints)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &TikvManager{client: client}, nil
// }

// type CommitTxn func(context.Context) error

// func (tm *TikvManager) WithTransaction(ctx context.Context) (*TikvTxnExec, CommitTxn, error) {
// 	txn, err := tm.client.Begin()
// 	if err != nil {
// 		return nil, func(ctx context.Context) error { return err }, err
// 	}
// 	exec := &TikvTxnExec{txn: txn}
// 	return exec, txn.Commit, nil
// }

// func (tm *TikvManager) Close() error {
// 	return tm.client.Close()
// }

// type TikvTxnExec struct {
// 	txn *transaction.KVTxn
// }

// // Fixed Get without accidental Rollback
// func (t *TikvTxnExec) Get(ctx context.Context, key []byte) ([]byte, error) {
// 	val, err := t.txn.Get(ctx, key)
// 	// if err == transaction {
// 	// 	return nil, ErrNotFound
// 	// }
// 	return val, err
// }

// // Updated Set to use KeyValue struct
// func (t *TikvTxnExec) Set(ctx context.Context, kv KeyValue) error {
// 	return t.txn.Set(kv.Key, kv.Value)
// }

// func (t *TikvTxnExec) Delete(ctx context.Context, key []byte) error {
// 	return t.txn.Delete(key)
// }

// func (t *TikvTxnExec) Rollback(ctx context.Context) error {
// 	return t.txn.Rollback()
// }

// // Batch operations
// func (t *TikvTxnExec) BatchSet(ctx context.Context, kvs []KeyValue) error {
// 	for _, kv := range kvs {
// 		if err := t.txn.Set(kv.Key, kv.Value); err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

// func (t *TikvTxnExec) BatchDelete(ctx context.Context, keys [][]byte) error {
// 	for _, key := range keys {
// 		if err := t.txn.Delete(key); err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

// func (t *TikvTxnExec) Exists(ctx context.Context, key []byte) (bool, error) {
// 	_, err := t.txn.Get(ctx, key)
// 	switch {
// 	case err == nil:
// 		return true, nil
// 	// case errors.Is(err, transaction.ErrNotExist):
// 	// 	return false, nil
// 	default:
// 		return false, err
// 	}
// }

// // Compare-and-Swap implementation
// func (t *TikvTxnExec) CompareAndSet(ctx context.Context, key, expected, newValue []byte) (bool, error) {
// 	current, err := t.txn.Get(ctx, key)
// 	switch {
// 	// case errors.Is(err, transaction.ErrNotExist):
// 	// 	if expected == nil {
// 	// 		return true, t.txn.Set(key, newValue)
// 	// 	}
// 	// 	return false, nil
// 	case err != nil:
// 		return false, err
// 	}

// 	if string(current) != string(expected) {
// 		return false, nil
// 	}
// 	return true, t.txn.Set(key, newValue)
// }

// // Atomic increment/decrement
// func (t *TikvTxnExec) Increment(ctx context.Context, key []byte, delta int64) (int64, error) {
// 	current, err := t.txn.Get(ctx, key)
// 	var value int64

// 	// if errors.Is(err, transaction.ErrNotExist) {
// 	// 	value = 0
// 	// } else
// 	if err != nil {
// 		return 0, err
// 	} else {
// 		if len(current) != 8 {
// 			return 0, ErrInvalidValue
// 		}
// 		value = int64(binary.BigEndian.Uint64(current))
// 	}

// 	newValue := value + delta
// 	buf := make([]byte, 8)
// 	binary.BigEndian.PutUint64(buf, uint64(newValue))
// 	return newValue, t.txn.Set(key, buf)
// }

// // Range scan implementation
// func (t *TikvTxnExec) Scan(ctx context.Context, startKey, endKey []byte, limit int) ([]KeyValue, error) {
// 	iter, err := t.txn.Iter(startKey, endKey)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer iter.Close()

// 	var results []KeyValue
// 	for iter.Valid() && len(results) < limit {
// 		results = append(results, KeyValue{
// 			Key:   iter.Key(),
// 			Value: iter.Value(),
// 		})
// 		if err := iter.Next(); err != nil {
// 			return nil, err
// 		}
// 	}
// 	return results, nil
// }

// // Prefix deletion using iterator
// func (t *TikvTxnExec) DeleteByPrefix(ctx context.Context, prefix []byte) error {
// 	endKey := append(prefix, 0xff)
// 	iter, err := t.txn.Iter(prefix, endKey)
// 	if err != nil {
// 		return err
// 	}
// 	defer iter.Close()

// 	var keys [][]byte
// 	for iter.Valid() {
// 		keys = append(keys, iter.Key())
// 		if err := iter.Next(); err != nil {
// 			return err
// 		}
// 	}

// 	for _, key := range keys {
// 		if err := t.txn.Delete(key); err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }
