//go:build openvino && openvino_genai

#include "ltx_video.hpp"

#include <algorithm>
#include <cmath>
#include <map>
#include <memory>
#include <stdexcept>

#include <openvino/core/preprocess/pre_post_process.hpp>
#include <openvino/openvino.hpp>
#include <openvino/genai/video_generation/autoencoder_kl_ltx_video.hpp>
#include <openvino/genai/video_generation/ltx_video_transformer_3d_model.hpp>

// GenAI 2026.2 exposes the scheduler factory publicly and its step interface in
// the pinned SDK sources. Use the export's scheduler for the noise schedule.
#include "image_generation/schedulers/ischeduler.hpp"

namespace {

using Path = std::filesystem::path;
using ov::Tensor;
using Decoder = ov::genai::AutoencoderKLLTXVideo;

// Defaults of the pinned GenAI LTX pipeline. Classifier-free guidance evaluates
// the empty prompt and the requested prompt in that order.
constexpr size_t guidance_batch = 2;
constexpr size_t prompt_length = 128;
constexpr float guidance_scale = 7.5f;

void check_cancelled(uintptr_t handle) {
    if (cx_ov_media_cancelled(handle)) {
        throw std::runtime_error("video generation cancelled");
    }
}

struct LatentLayout {
    size_t frames, height, width, channels;
    size_t spatial_scale, temporal_scale;

    size_t tokens() const { return frames * height * width; }
    ov::Shape packed(size_t batch) const { return {batch, tokens(), channels}; }
};

LatentLayout latent_layout(const Path &dir, const Decoder::Config &vae, const cx_ov_media_settings &settings) {
    const ov::genai::LTXVideoTransformer3DModel::Config transformer(dir / "transformer/config.json");
    OPENVINO_ASSERT(transformer.patch_size == 1 && transformer.patch_size_t == 1,
                    "LTX requires a unit-patch transformer");
    OPENVINO_ASSERT(!vae.timestep_conditioning, "LTX does not support timestep-conditioned VAE decoding");
    OPENVINO_ASSERT(vae.latent_channels > 0 && vae.latent_channels == transformer.in_channels &&
                    vae.latents_std_data.size() == vae.latent_channels &&
                    vae.latents_mean_data.size() == vae.latent_channels && vae.scaling_factor > 0,
                    "inconsistent LTX latent channel configuration");

    const size_t height = settings.height, width = settings.width, intervals = settings.frames - 1;
    const auto levels = std::count(vae.spatio_temporal_scaling.begin(), vae.spatio_temporal_scaling.end(), true);
    OPENVINO_ASSERT(levels < 8 && vae.patch_size > 0 && vae.patch_size <= std::min(height, width) &&
                    vae.patch_size_t > 0 && vae.patch_size_t <= intervals, "invalid LTX VAE compression");
    const size_t spatial = vae.patch_size << levels;
    const size_t temporal = vae.patch_size_t << levels;
    OPENVINO_ASSERT(width % spatial == 0 && height % spatial == 0 && intervals % temporal == 0,
                    "requested video dimensions do not fit the LTX VAE");
    return {intervals / temporal + 1, height / spatial, width / spatial, vae.latent_channels, spatial, temporal};
}

struct PromptTokens {
    Tensor ids{ov::element::i64, {guidance_batch, prompt_length}};
    Tensor mask{ov::element::i64, {guidance_batch, prompt_length}};
};

// The export implements truncation itself. Running GenAI 2026.2's tokenizer
// rewrite over an already-stateful Truncate can dereference a missing constant.
PromptTokens tokenize(ov::Core &core, const Path &dir, const std::string &prompt) {
    const auto graph = core.read_model(dir / "tokenizer/openvino_tokenizer.xml");
    const auto pad = graph->has_rt_info("pad_token_id") ? graph->get_rt_info<int64_t>("pad_token_id") : 0;
    const auto eos = graph->has_rt_info("eos_token_id") ? graph->get_rt_info<int64_t>("eos_token_id") : 1;
    auto tokenizer = core.compile_model(graph, "CPU").create_infer_request();
    Tensor prompts(ov::element::string, {guidance_batch});
    prompts.data<std::string>()[0] = "";
    prompts.data<std::string>()[1] = prompt;
    tokenizer.set_input_tensor(prompts);
    tokenizer.infer();

    const auto ids = tokenizer.get_tensor("input_ids");
    const auto mask = tokenizer.get_tensor("attention_mask");
    const auto shape = ids.get_shape();
    OPENVINO_ASSERT(shape.size() == 2 && shape[0] == guidance_batch && mask.get_shape() == shape,
                    "LTX tokenizer returned incompatible token and attention shapes");
    const auto *token_ids = ids.data<const int64_t>();
    const auto *attention = mask.data<const int64_t>();
    PromptTokens result;
    std::fill_n(result.ids.data<int64_t>(), result.ids.get_size(), pad);
    std::fill_n(result.mask.data<int64_t>(), result.mask.get_size(), 0);
    for (size_t row = 0; row < guidance_batch; ++row) {
        auto *dest = result.ids.data<int64_t>() + row * prompt_length;
        size_t count = 0;
        for (size_t col = 0; col < shape[1] && count < prompt_length; ++col) {
            const size_t index = row * shape[1] + col;
            if (attention[index]) {
                dest[count++] = token_ids[index];
            }
        }
        // Retain EOS at the truncation boundary, including for the empty prompt.
        if (!count || dest[count - 1] != eos) {
            count = std::min(count + 1, prompt_length);
            dest[count - 1] = eos;
        }
        std::fill_n(result.mask.data<int64_t>() + row * prompt_length, count, 1);
    }
    return result;
}

struct Conditioning {
    Tensor embeddings, mask;
};

Conditioning encode_prompt(ov::Core &core, const Path &dir, const std::string &device, const PromptTokens &tokens) {
    auto graph = core.read_model(dir / "text_encoder/openvino_model.xml");
    std::map<std::string, ov::PartialShape> shapes;
    ov::preprocess::PrePostProcessor io(graph);
    for (const auto &input : graph->inputs()) {
        const auto name = input.get_any_name();
        OPENVINO_ASSERT(name == "input_ids" || name == "attention_mask", "unsupported LTX T5 input: ", name);
        shapes[name] = tokens.ids.get_shape();
        io.input(name).tensor().set_element_type(ov::element::i64);
    }
    io.output().tensor().set_element_type(ov::element::f32);
    graph = io.build();
    graph->reshape(shapes);
    auto encoder = core.compile_model(graph, device).create_infer_request();
    encoder.set_tensor("input_ids", tokens.ids);
    if (shapes.count("attention_mask")) {
        encoder.set_tensor("attention_mask", tokens.mask);
    }
    encoder.infer();
    const auto output = encoder.get_output_tensor();
    Tensor embeddings(ov::element::f32, output.get_shape());
    output.copy_to(embeddings);
    return {embeddings, tokens.mask};
}

void set_integer(ov::InferRequest &request, const char *name, int64_t value) {
    Tensor scalar(ov::element::i64, {});
    scalar.data<int64_t>()[0] = value;
    request.set_tensor(name, scalar);
}

ov::InferRequest prepare_transformer(ov::Core &core, const Path &dir, const std::string &device,
                                     const Conditioning &text, const LatentLayout &layout, int fps) {
    auto graph = core.read_model(dir / "transformer/openvino_model.xml");
    // Older exports consume one timestep. Newer LTX exports consume a value
    // for every video token, which Text2VideoPipeline in GenAI 2026.2 cannot bind.
    const auto rank = graph->input("timestep").get_partial_shape().rank();
    OPENVINO_ASSERT(rank == 1 || rank == 2, "unsupported LTX timestep rank: ", rank);
    const ov::Shape timesteps = rank == 2 ? ov::Shape{guidance_batch, layout.tokens()} : ov::Shape{1};
    graph->reshape(std::map<std::string, ov::PartialShape>{
        {"hidden_states", layout.packed(guidance_batch)}, {"timestep", timesteps},
        {"encoder_hidden_states", text.embeddings.get_shape()}, {"encoder_attention_mask", text.mask.get_shape()}});
    auto request = core.compile_model(graph, device).create_infer_request();
    request.set_tensor("encoder_hidden_states", text.embeddings);
    request.set_tensor("encoder_attention_mask", text.mask);
    set_integer(request, "num_frames", layout.frames);
    set_integer(request, "height", layout.height);
    set_integer(request, "width", layout.width);
    auto rope = request.get_tensor("rope_interpolation_scale");
    OPENVINO_ASSERT(rope.get_shape() == ov::Shape{3}, "invalid LTX rotary position scale");
    auto *scale = rope.data<float>();
    scale[0] = static_cast<float>(layout.temporal_scale) / fps;
    scale[1] = scale[2] = static_cast<float>(layout.spatial_scale);
    return request;
}

Tensor sample(ov::InferRequest &transformer, const Path &dir, const LatentLayout &layout,
              const cx_ov_media_settings &settings, uintptr_t cancel_handle) {
    auto scheduler = std::dynamic_pointer_cast<ov::genai::IScheduler>(
        ov::genai::Scheduler::from_config(dir / "scheduler/scheduler_config.json"));
    OPENVINO_ASSERT(scheduler, "invalid LTX scheduler");
    scheduler->set_timesteps(layout.tokens(), settings.steps, 1.f);
    const auto timesteps = scheduler->get_float_timesteps();
    OPENVINO_ASSERT(!timesteps.empty() && std::all_of(timesteps.begin(), timesteps.end(),
                    [](float t) { return std::isfinite(t); }), "invalid LTX noise schedule");

    auto generator = std::make_shared<ov::genai::CppStdGenerator>(settings.seed);
    Tensor latent = generator->randn_tensor(layout.packed(1));
    Tensor residual(ov::element::f32, latent.get_shape());
    auto input = transformer.get_tensor("hidden_states");
    auto timestep = transformer.get_tensor("timestep");
    const size_t count = residual.get_size();
    for (size_t step = 0; step < timesteps.size(); ++step) {
        check_cancelled(cancel_handle);
        OPENVINO_ASSERT(latent.get_shape() == residual.get_shape(), "invalid LTX scheduler output shape");
        auto *batch = input.data<float>();
        std::copy_n(latent.data<const float>(), count, batch);
        std::copy_n(latent.data<const float>(), count, batch + count);
        std::fill_n(timestep.data<float>(), timestep.get_size(), timesteps[step]);
        transformer.infer();
        const auto prediction = transformer.get_output_tensor();
        OPENVINO_ASSERT(prediction.get_shape() == input.get_shape(), "invalid LTX transformer output shape");
        const auto *negative = prediction.data<const float>();
        const auto *positive = negative + count;
        auto *guided = residual.data<float>();
        for (size_t i = 0; i < count; ++i) {
            guided[i] = negative[i] + guidance_scale * (positive[i] - negative[i]);
        }
        latent = scheduler->step(residual, latent, step, generator).at("latent");
    }
    return latent;
}

// Transformer order is [1, tokens, channels]. The VAE expects [1, channels, frames, height, width].
Tensor decode(const Path &dir, const std::string &device, const cx_ov_media_settings &settings,
              const LatentLayout &layout, const Decoder::Config &vae, const Tensor &packed) {
    OPENVINO_ASSERT(packed.get_shape() == layout.packed(1), "invalid LTX decoder input shape");
    Tensor latent(ov::element::f32, {1, layout.channels, layout.frames, layout.height, layout.width});
    const auto *source = packed.data<const float>();
    auto *destination = latent.data<float>();
    const size_t tokens = layout.tokens();
    for (size_t channel = 0; channel < layout.channels; ++channel) {
        const float scale = vae.latents_std_data[channel] / vae.scaling_factor;
        for (size_t token = 0; token < tokens; ++token) {
            destination[channel * tokens + token] =
                source[token * layout.channels + channel] * scale + vae.latents_mean_data[channel];
        }
    }
    Decoder decoder(dir / "vae_decoder");
    decoder.reshape(1, settings.frames, settings.height, settings.width).compile(device);
    return decoder.decode(latent);
}

} // namespace

ov::Tensor cx_ov_render_ltx(const Path &dir, const Path &tokenizers, const std::string &device,
                            const std::string &prompt, const cx_ov_media_settings &settings, uintptr_t cancel_handle) {
    const Decoder::Config vae(dir / "vae_decoder/config.json");
    const auto layout = latent_layout(dir, vae, settings);
    ov::Core core;
    core.add_extension(tokenizers);

    // Keep only one compiled stage resident. The T5 weights alone are several GB.
    check_cancelled(cancel_handle);
    const auto text = encode_prompt(core, dir, device, tokenize(core, dir, prompt));
    check_cancelled(cancel_handle);
    Tensor latent;
    {
        auto transformer = prepare_transformer(core, dir, device, text, layout, settings.fps);
        latent = sample(transformer, dir, layout, settings, cancel_handle);
    }
    check_cancelled(cancel_handle);
    return decode(dir, device, settings, layout, vae, latent);
}
