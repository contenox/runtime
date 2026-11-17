package capacity

// NativeHostOverheadBytes reserves host staging and process buffers beyond model and KV storage.
const NativeHostOverheadBytes int64 = 64 << 20
