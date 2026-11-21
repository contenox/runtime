#pragma once

#include "media.h"
#include <filesystem>
#include <string>
#include <openvino/runtime/tensor.hpp>

// Returns an owned uint8 tensor with shape [1, frames, height, width, 3].
ov::Tensor cx_ov_render_ltx(const std::filesystem::path &directory, const std::filesystem::path &tokenizers,
                            const std::string &device, const std::string &prompt,
                            const cx_ov_media_settings &settings, uintptr_t cancel_handle);
