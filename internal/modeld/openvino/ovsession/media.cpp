//go:build openvino && openvino_genai

#include "media.h"
#include "ltx_video.hpp"

#include <cstdio>
#include <cstring>
#include <filesystem>
#include <stdexcept>
#include <openvino/genai/image_generation/text2image_pipeline.hpp>
#include <openvino/genai/video_generation/text2video_pipeline.hpp>

namespace {

// Keep compatible exports on the public GenAI pipeline just like image generation.
ov::AnyMap generation_settings(const cx_ov_media_settings &settings, uintptr_t cancel_handle) {
    auto stop = [cancel_handle](size_t, size_t, ov::Tensor &) {
        return cx_ov_media_cancelled(cancel_handle) != 0;
    };
    return {ov::genai::num_inference_steps(settings.steps), ov::genai::width(settings.width),
            ov::genai::height(settings.height), ov::genai::callback(stop)};
}

} // namespace

extern "C" int cx_ov_render_media(const char *model_dir, const char *tokenizers, const char *device, const char *prompt,
                                  cx_ov_media_settings settings, int use_ltx_adapter, uintptr_t cancel_handle,
                                  uint8_t *rgb, size_t rgb_size, char *error, size_t error_size) {
    try {
        if (!model_dir || !device || !prompt || !rgb || !cancel_handle ||
            settings.width <= 0 || settings.height <= 0 || settings.frames <= 0) {
            throw std::invalid_argument("invalid OpenVINO media request");
        }
        const auto directory = std::filesystem::u8path(model_dir);
        ov::Shape shape{1};
        ov::Tensor pixels;
        if (settings.frames == 1) {
            ov::genai::Text2ImagePipeline pipeline(directory);
            pipeline.reshape(1, settings.height, settings.width, pipeline.get_generation_config().guidance_scale);
            pipeline.compile(device);
            auto options = generation_settings(settings, cancel_handle);
            options.insert(ov::genai::rng_seed(settings.seed));
            pixels = pipeline.generate(prompt, options);
        } else {
            shape.push_back(static_cast<size_t>(settings.frames));
            if (use_ltx_adapter) {
                if (!tokenizers || !*tokenizers) {
                    throw std::invalid_argument("OpenVINO tokenizer extension is missing from the worker bundle");
                }
                pixels = cx_ov_render_ltx(directory, std::filesystem::u8path(tokenizers), device, prompt, settings, cancel_handle);
            } else {
                ov::genai::Text2VideoPipeline pipeline(directory);
                pipeline.reshape(1, settings.frames, settings.height, settings.width, pipeline.get_generation_config().guidance_scale);
                pipeline.compile(device);
                auto options = generation_settings(settings, cancel_handle);
                options.insert(ov::genai::num_frames(settings.frames));
                options.insert(ov::genai::frame_rate(static_cast<float>(settings.fps)));
                options.insert(ov::genai::generator(std::make_shared<ov::genai::CppStdGenerator>(settings.seed)));
                pixels = pipeline.generate(prompt, options).video;
            }
        }
        if (cx_ov_media_cancelled(cancel_handle)) {
            return 1;
        }
        shape.insert(shape.end(), {static_cast<size_t>(settings.height), static_cast<size_t>(settings.width), 3});
        if (pixels.get_element_type() != ov::element::u8 || pixels.get_shape() != shape ||
            !pixels.is_continuous() || pixels.get_byte_size() != rgb_size) {
            throw std::runtime_error("diffusion pipeline returned an unexpected pixel layout");
        }
        std::memcpy(rgb, pixels.data<const uint8_t>(), rgb_size);
        return 0;
    } catch (const std::exception &e) {
        if (error && error_size) std::snprintf(error, error_size, "%s", e.what());
    } catch (...) {
        if (error && error_size) std::snprintf(error, error_size, "OpenVINO media generation failed");
    }
    return -1;
}
