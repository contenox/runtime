//go:build llamacpp_direct

package llamacppshim

/*
#include <stdlib.h>
#include "gguf.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// ValidateAdapterPair reads the two GGUF headers before publishing a variant.
// llama.cpp also checks individual tensor shapes when the variant is loaded.
func ValidateAdapterPair(base, adapter string) error {
	read := func(path string) (map[string]string, error) {
		name := C.CString(path)
		defer C.free(unsafe.Pointer(name))
		header := C.gguf_init_from_file(name, C.struct_gguf_init_params{no_alloc: true})
		if header == nil {
			return nil, fmt.Errorf("cannot read GGUF metadata from %s", path)
		}
		defer C.gguf_free(header)
		fields := map[string]string{}
		for _, key := range []string{"general.architecture", "general.type", "adapter.type"} {
			ckey := C.CString(key)
			id := C.gguf_find_key(header, ckey)
			C.free(unsafe.Pointer(ckey))
			if id >= 0 && C.gguf_get_kv_type(header, id) == C.GGUF_TYPE_STRING {
				fields[key] = C.GoString(C.gguf_get_val_str(header, id))
			}
		}
		return fields, nil
	}
	model, err := read(base)
	if err != nil {
		return err
	}
	weights, err := read(adapter)
	if err != nil {
		return err
	}
	if model["general.type"] == "adapter" || model["general.architecture"] == "" {
		return errors.New("choose a base GGUF model for this adapter")
	}
	if weights["general.type"] != "adapter" || weights["adapter.type"] != "lora" {
		return errors.New("the selected file is not a GGUF LoRA adapter")
	}
	if a, b := model["general.architecture"], weights["general.architecture"]; a != b {
		return fmt.Errorf("the %s adapter is incompatible with the %s base model", b, a)
	}
	return nil
}
