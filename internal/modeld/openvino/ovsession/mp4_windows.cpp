//go:build windows && openvino && openvino_genai

#include "media.h"

#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mfreadwrite.h>
#include <wrl/client.h>

namespace {

using Microsoft::WRL::ComPtr;

void check(HRESULT hr) {
    if (FAILED(hr)) {
        throw hr;
    }
}

ComPtr<IMFMediaType> video_type(const GUID &subtype, const cx_ov_media_settings &settings) {
    ComPtr<IMFMediaType> type;
    check(MFCreateMediaType(&type));
    check(type->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video));
    check(type->SetGUID(MF_MT_SUBTYPE, subtype));
    check(MFSetAttributeSize(type.Get(), MF_MT_FRAME_SIZE, settings.width, settings.height));
    check(MFSetAttributeRatio(type.Get(), MF_MT_FRAME_RATE, settings.fps, 1));
    check(MFSetAttributeRatio(type.Get(), MF_MT_PIXEL_ASPECT_RATIO, 1, 1));
    check(type->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive));
    return type;
}

// BT.601 studio-range NV12 has a full luma plane followed by interleaved UV.
// Each 2x2 group of RGB pixels contributes four Y samples and one UV pair.
void fill_nv12(const uint8_t *rgb, uint8_t *luma, int width, int height) noexcept {
    auto *chroma = luma + width * height;
    for (int y = 0; y < height; y += 2) {
        for (int x = 0; x < width; x += 2) {
            int red = 0, green = 0, blue = 0;
            for (int row = y; row < y + 2; ++row) {
                for (int col = x; col < x + 2; ++col) {
                    const int pixel = row * width + col;
                    const uint8_t *color = rgb + 3 * pixel;
                    const int r = color[0], g = color[1], b = color[2];
                    luma[pixel] = static_cast<uint8_t>(16 + ((66*r + 129*g + 25*b + 128) >> 8));
                    red += r;
                    green += g;
                    blue += b;
                }
            }
            const int uv = y / 2 * width + x;
            chroma[uv] = static_cast<uint8_t>(128 + ((-38*red - 74*green + 112*blue + 512) >> 10));
            chroma[uv + 1] = static_cast<uint8_t>(128 + ((112*red - 94*green - 18*blue + 512) >> 10));
        }
    }
}

ComPtr<IMFSample> video_sample(const uint8_t *rgb, const cx_ov_media_settings &settings, int frame) {
    const DWORD bytes = settings.width * settings.height * 3 / 2;
    ComPtr<IMFMediaBuffer> buffer;
    check(MFCreateMemoryBuffer(bytes, &buffer));
    BYTE *pixels;
    check(buffer->Lock(&pixels, nullptr, nullptr));
    fill_nv12(rgb, pixels, settings.width, settings.height);
    check(buffer->Unlock());
    check(buffer->SetCurrentLength(bytes));

    constexpr LONGLONG ticks_per_second = 10000000; // Media Foundation time uses 100 ns.
    const auto start = frame * ticks_per_second / settings.fps;
    const auto end = (frame + 1) * ticks_per_second / settings.fps;
    ComPtr<IMFSample> sample;
    check(MFCreateSample(&sample));
    check(sample->AddBuffer(buffer.Get()));
    check(sample->SetSampleTime(start));
    check(sample->SetSampleDuration(end - start));
    return sample;
}

void encode_frames(const wchar_t *path, const uint8_t *rgb, const cx_ov_media_settings &settings, uintptr_t handle) {
    ComPtr<IMFSinkWriter> writer;
    check(MFCreateSinkWriterFromURL(path, nullptr, nullptr, &writer));
    auto output = video_type(MFVideoFormat_H264, settings);
    check(output->SetUINT32(MF_MT_AVG_BITRATE, settings.width * settings.height * settings.fps * 2));
    DWORD stream;
    check(writer->AddStream(output.Get(), &stream));
    auto input = video_type(MFVideoFormat_NV12, settings);
    check(writer->SetInputMediaType(stream, input.Get(), nullptr));
    check(writer->BeginWriting());
    const size_t stride = static_cast<size_t>(settings.width) * settings.height * 3;
    for (int frame = 0; frame < settings.frames; ++frame) {
        if (cx_ov_media_cancelled(handle)) {
            throw static_cast<HRESULT>(E_ABORT);
        }
        auto sample = video_sample(rgb + frame * stride, settings, frame);
        check(writer->WriteSample(stream, sample.Get()));
    }
    check(writer->Finalize());
}

} // namespace

extern "C" int cx_ov_encode_mp4(const uint16_t *path, const uint8_t *rgb, size_t rgb_size,
                                cx_ov_media_settings settings, uintptr_t handle) {
    // Go validates media settings before entering the codec. Check the frame buffer
    // again at the ABI boundary before traversing it in 2x2 pixel groups.
    if (!path || !rgb || !handle || settings.width <= 0 || settings.width % 2 ||
        settings.height <= 0 || settings.height % 2 || settings.fps <= 0 || settings.frames <= 0 ||
        rgb_size != static_cast<size_t>(settings.width) * settings.height * settings.frames * 3) {
        return E_INVALIDARG;
    }
    const HRESULT com = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    if (FAILED(com) && com != RPC_E_CHANGED_MODE) {
        return com;
    }
    HRESULT hr = MFStartup(MF_VERSION);
    if (SUCCEEDED(hr)) {
        try {
            encode_frames(reinterpret_cast<const wchar_t *>(path), rgb, settings, handle);
        } catch (HRESULT error) {
            hr = error;
        } catch (...) {
            hr = E_FAIL;
        }
        MFShutdown();
    }
    if (SUCCEEDED(com)) {
        CoUninitialize();
    }
    return hr;
}
