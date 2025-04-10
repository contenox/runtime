package libkv_test

// // TestKVSetGet exercises the key-value manager by setting a key, retrieving it,
// // checking existence, listing keys, and finally deleting the key.
// func TestKVSetGet(t *testing.T) {
// 	ctx := context.Background()
// 	url, container, cleanup, err := libkv.SetupNatsInstance(ctx)
// 	defer cleanup()
// 	require.NoError(t, err)
// 	require.True(t, container.IsRunning())

// 	// Create a new KV manager using a test bucket name.
// 	manager, err := libkv.NewNatsKVManager(url, "TEST_BUCKET", true)
// 	require.NoError(t, err)
// 	defer manager.Close()

// 	exec, err := manager.Operation(ctx)
// 	require.NoError(t, err)

// 	key := []byte("testKey")
// 	value := []byte("testValue")

// 	// Set the key-value pair.
// 	err = exec.Set(ctx, libkv.KeyValue{Key: key, Value: value})
// 	require.NoError(t, err)

// 	// Get the key and verify the value.
// 	retValue, err := exec.Get(ctx, key)
// 	require.NoError(t, err)
// 	require.Equal(t, value, retValue)

// 	// Check that the key exists.
// 	exists, err := exec.Exists(ctx, key)
// 	require.NoError(t, err)
// 	require.True(t, exists)

// 	// List keys and ensure our key is present.
// 	keys, err := exec.List(ctx)
// 	require.NoError(t, err)
// 	found := false
// 	for _, k := range keys {
// 		if k == string(key) {
// 			found = true
// 			break
// 		}
// 	}
// 	require.True(t, found, "expected key not found in list")

// 	// Delete the key.
// 	err = exec.Delete(ctx, key)
// 	require.NoError(t, err)

// 	// After deletion, Get should return ErrNotFound.
// 	_, err = exec.Get(ctx, key)
// 	require.Error(t, err)
// 	require.Equal(t, libkv.ErrNotFound, err)

// 	// And Exists should return false.
// 	exists, err = exec.Exists(ctx, key)
// 	require.NoError(t, err)
// 	require.False(t, exists)
// }

// // TestOverwrite verifies that setting a key twice overwrites the previous value.
// func TestOverwrite(t *testing.T) {
// 	ctx := context.Background()
// 	url, container, cleanup, err := libkv.SetupNatsInstance(ctx)
// 	defer cleanup()
// 	require.NoError(t, err)
// 	require.True(t, container.IsRunning())

// 	manager, err := libkv.NewNatsKVManager(url, "TEST_BUCKET_OVERWRITE", true)
// 	require.NoError(t, err)
// 	defer manager.Close()

// 	exec, err := manager.Operation(ctx)
// 	require.NoError(t, err)

// 	key := []byte("testKeyOverwrite")
// 	firstValue := []byte("first")
// 	secondValue := []byte("second")

// 	// Set the key with the first value.
// 	err = exec.Set(ctx, libkv.KeyValue{Key: key, Value: firstValue})
// 	require.NoError(t, err)

// 	// Verify the first value.
// 	retValue, err := exec.Get(ctx, key)
// 	require.NoError(t, err)
// 	require.Equal(t, firstValue, retValue)

// 	// Overwrite the key with the second value.
// 	err = exec.Set(ctx, libkv.KeyValue{Key: key, Value: secondValue})
// 	require.NoError(t, err)

// 	// Verify the updated value.
// 	retValue, err = exec.Get(ctx, key)
// 	require.NoError(t, err)
// 	require.Equal(t, secondValue, retValue)
// }
