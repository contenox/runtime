#ifndef CONTENOX_OV_MEDIA_H
#define CONTENOX_OV_MEDIA_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    int width;
    int height;
    int frames;
    int fps;
    int steps;
    uint32_t seed;
} cx_ov_media_settings;

// Synchronous. The caller owns the UTF-8 strings and both buffers for the
// duration of the call. On success rgb contains tightly packed RGB24 frames.
int cx_ov_render_media(const char *model_dir, const char *tokenizers, const char *device, const char *prompt,
                       cx_ov_media_settings settings, int use_ltx_adapter, uintptr_t cancel_handle,
                       uint8_t *rgb, size_t rgb_size, char *error, size_t error_size);
int cx_ov_media_cancelled(uintptr_t handle);

#ifdef _WIN32
int cx_ov_encode_mp4(const uint16_t *path, const uint8_t *rgb, size_t rgb_size,
                     cx_ov_media_settings settings, uintptr_t cancel_handle);
#endif

#ifdef __cplusplus
}
#endif
#endif
