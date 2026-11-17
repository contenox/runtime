import importlib.util
import json
from pathlib import Path
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("build-openvino-literal-source-companion.py")
SPEC = importlib.util.spec_from_file_location("build_openvino_literal_source_companion", SCRIPT)
BUILDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILDER)
PREPARER = BUILDER.load_preparer()


def convert_fixture(companion_dir):
    (companion_dir / "openvino_tokenizer.xml").write_bytes(b"tokenizer-ir")
    (companion_dir / "openvino_tokenizer.bin").write_bytes(b"tokenizer-weights")


TOOLCHAIN = {"python": "3.13.0", "packages": {"openvino": "2026.2.0", "openvino-tokenizers": "2026.2.0.0", "transformers": "5.0.0", "tokenizers": "0.22.2"}}


def starcoder_requirement(asset_id="starcoder2-7b-fim-test-v1"):
    return {
        "assetId": asset_id,
        "model": "starcoder2-7b-fim-ov",
        "modelRepo": "OpenVINO/starcoder2-7b-int4-ov",
        "modelRevision": "60af611c55a94e4ce5c11c63eff2401642474d93",
        "kind": "fim",
        "format": "starcoder",
        "version": 1,
        "controls": PREPARER.CONTROLS["starcoder"],
    }


class LiteralSourceCompanionBuildTest(unittest.TestCase):
    def snapshot(self, root):
        model = root / "snapshot"
        model.mkdir()
        tokenizer = {
            "model": {"type": "BPE", "vocab": {"ordinary": 0, "<fim_prefix>": 1}, "merges": []},
            "added_tokens": [{"id": 2, "content": "<fim_suffix>", "special": True}],
        }
        (model / "tokenizer.json").write_text(json.dumps(tokenizer), encoding="utf-8")
        (model / "LICENSE").write_bytes(b"companion license\n")
        return model

    def test_build_publishes_one_archive_matching_the_format_schema(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            model, assets = self.snapshot(root), root / "assets"
            archive = BUILDER.build(model, starcoder_requirement(), assets, converter=convert_fixture, toolchain=TOOLCHAIN)
            self.assertEqual(archive, assets / "starcoder2-7b-fim-test-v1.tar.gz")
            with tarfile.open(archive) as packed:
                names = sorted(packed.getnames())
                self.assertEqual(names, sorted([
                    PREPARER.MANIFEST,
                    f"{PREPARER.COMPANION}/tokenizer.json",
                    f"{PREPARER.COMPANION}/tokenizer_config.json",
                    f"{PREPARER.COMPANION}/openvino_tokenizer.xml",
                    f"{PREPARER.COMPANION}/openvino_tokenizer.bin",
                    f"{PREPARER.COMPANION}/LICENSE",
                    f"{PREPARER.COMPANION}/PROVENANCE.json",
                ]))
                manifest = json.loads(packed.extractfile(PREPARER.MANIFEST).read().decode("utf-8"))
                provenance = json.loads(packed.extractfile(f"{PREPARER.COMPANION}/PROVENANCE.json").read().decode("utf-8"))
            self.assertEqual(manifest["kind"], "fim")
            self.assertEqual(manifest["format"], "starcoder")
            self.assertEqual(manifest["format_version"], 1)
            self.assertEqual(manifest["control_tokens"], PREPARER.CONTROLS["starcoder"])
            self.assertEqual(manifest["identity"], PREPARER.identity({key: value for key, value in manifest.items() if key != "identity"}))
            self.assertEqual(provenance["asset_id"], "starcoder2-7b-fim-test-v1")
            self.assertEqual(provenance["model"]["revision"], "60af611c55a94e4ce5c11c63eff2401642474d93")
            self.assertEqual(provenance["license"]["sha256"], PREPARER.digest(b"companion license\n"))
            self.assertNotIn("<fim_prefix>", json.loads(
                tarfile.open(archive).extractfile(f"{PREPARER.COMPANION}/tokenizer.json").read().decode("utf-8")
            )["model"]["vocab"])

    def test_rebuild_needs_an_explicit_replacement_and_is_reproducible(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            model, assets = self.snapshot(root), root / "assets"
            first = BUILDER.build(model, starcoder_requirement(), assets, converter=convert_fixture, toolchain=TOOLCHAIN)
            published = first.read_bytes()
            with self.assertRaisesRegex(ValueError, "already exists"):
                BUILDER.build(model, starcoder_requirement(), assets, converter=convert_fixture, toolchain=TOOLCHAIN)
            rebuilt = BUILDER.build(model, starcoder_requirement(), assets, converter=convert_fixture, toolchain=TOOLCHAIN, force=True)
            self.assertEqual(rebuilt.read_bytes(), published)

    def test_a_requirement_disagreeing_with_the_schema_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            model, assets = self.snapshot(root), root / "assets"
            for field, value in (("format", "declared-nowhere"), ("kind", "next-edit"), ("version", 9), ("controls", ["<|only|>"])):
                requirement = starcoder_requirement()
                requirement[field] = value
                with self.assertRaisesRegex(ValueError, "format schema declares|declares no literal-source"):
                    BUILDER.build(model, requirement, assets, converter=convert_fixture, toolchain=TOOLCHAIN)

    def test_a_snapshot_without_its_license_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            model, assets = self.snapshot(root), root / "assets"
            (model / "LICENSE").unlink()
            with self.assertRaisesRegex(ValueError, "missing model license"):
                BUILDER.build(model, starcoder_requirement(), assets, converter=convert_fixture, toolchain=TOOLCHAIN)


if __name__ == "__main__":
    unittest.main()
