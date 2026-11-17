import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("prepare-openvino-literal-source-tokenizer.py")
SPEC = importlib.util.spec_from_file_location("prepare_openvino_literal_source_tokenizer", SCRIPT)
PREPARER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PREPARER)


class LiteralSourceTokenizerPreparationTest(unittest.TestCase):
    def convert_fixture(self, companion_dir):
        (companion_dir / "openvino_tokenizer.xml").write_bytes(b"tokenizer-ir")
        (companion_dir / "openvino_tokenizer.bin").write_bytes(b"tokenizer-weights")

    def prepare_fixture(self, model, fmt):
        return PREPARER.prepare(
            model,
            fmt,
            converter=self.convert_fixture,
            toolchain={"python": "3.13.0", "packages": {"openvino": "2026.2.0", "openvino-tokenizers": "2026.2.0.0", "transformers": "5.0.0", "tokenizers": "0.22.2"}},
        )

    def test_companion_preserves_vocab_ids_and_removes_fim_control_ids(self):
        with tempfile.TemporaryDirectory() as temporary:
            model = Path(temporary) / "model"
            model.mkdir()
            tokenizer = {
                "version": "1.0",
                "model": {
                    "type": "BPE",
                    "vocab": {"ordinary": 0, "<fim_prefix>": 1, "<fim_middle>": 2, "<|other_special|>": 5},
                    "merges": [],
                },
                "added_tokens": [
                    {"id": 3, "content": "<fim_suffix>", "special": True},
                    {"id": 5, "content": "<|other_special|>", "special": True},
                    {"id": 4, "content": "ordinary-added", "special": False},
                ],
            }
            (model / "tokenizer.json").write_text(json.dumps(tokenizer), encoding="utf-8")

            manifest_path = self.prepare_fixture(model, "starcoder")
            companion = json.loads((model / PREPARER.COMPANION / "tokenizer.json").read_text(encoding="utf-8"))
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            vocab = companion["model"]["vocab"]
            added = companion["added_tokens"]

            self.assertEqual(vocab["ordinary"], 0)
            self.assertNotIn("<fim_prefix>", vocab)
            self.assertNotIn("<fim_middle>", vocab)
            self.assertNotIn("<|other_special|>", vocab)
            self.assertEqual([entry["id"] for entry in added], [4])
            self.assertNotIn("<fim_suffix>", [entry["content"] for entry in added])
            self.assertNotIn("<|other_special|>", [entry["content"] for entry in added])
            self.assertEqual(added[0]["content"], "ordinary-added")
            self.assertEqual(set(vocab.values()), {0})
            self.assertEqual(manifest["removed_tokens"], sorted({*PREPARER.CONTROLS["starcoder"], "<|other_special|>"}))
            self.assertNotIn(3, [entry["id"] for entry in added])
            self.assertEqual(manifest["identity"], PREPARER.identity(
                {key: value for key, value in manifest.items() if key != "identity"},
            ))
            self.assertEqual(manifest["tokenizer_ir_sha256"], PREPARER.digest(b"tokenizer-ir"))
            self.assertEqual(manifest["tokenizer_weights_sha256"], PREPARER.digest(b"tokenizer-weights"))

    def test_next_edit_format_prepares_its_control_vocabulary(self):
        with tempfile.TemporaryDirectory() as temporary:
            model = Path(temporary)
            tokenizer = {"model": {"type": "BPE", "vocab": {"ordinary": 0, "<|file_sep|>": 1}, "merges": []}, "added_tokens": []}
            (model / "tokenizer.json").write_text(json.dumps(tokenizer), encoding="utf-8")
            manifest_path = self.prepare_fixture(model, "sweep-v2")
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            self.assertEqual(manifest["kind"], "next-edit")
            self.assertEqual(manifest["control_tokens"], ["<|file_sep|>", "<|endoftext|>"])
            companion_vocab = json.loads((model / PREPARER.COMPANION / "tokenizer.json").read_text(encoding="utf-8"))["model"]["vocab"]
            self.assertNotIn("<|file_sep|>", companion_vocab)
            self.assertEqual(set(companion_vocab.values()), {0})

    def test_existing_companion_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as temporary:
            model = Path(temporary)
            tokenizer = {"model": {"type": "BPE", "vocab": {}, "merges": []}, "added_tokens": []}
            (model / "tokenizer.json").write_text(json.dumps(tokenizer), encoding="utf-8")
            (model / PREPARER.MANIFEST).write_text("kept", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "already exists"):
                self.prepare_fixture(model, "starcoder")
            self.assertEqual((model / PREPARER.MANIFEST).read_text(encoding="utf-8"), "kept")


if __name__ == "__main__":
    unittest.main()
