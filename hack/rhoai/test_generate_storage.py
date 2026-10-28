# /// script
# requires-python = ">=3.11"
# dependencies = ["tomlkit==0.13.2"]
# ///

"""Tests for the RHOAI storage-initializer artifact generator contract."""

from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import textwrap
import unittest

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
GENERATOR_PATH = Path(__file__).with_name("generate_storage.py")
WORKFLOW_PATH = REPOSITORY_ROOT / ".github/workflows/storage-rhoai-update.yml"

spec = importlib.util.spec_from_file_location("generate_storage", GENERATOR_PATH)
if spec is None or spec.loader is None:
    raise RuntimeError(f"cannot load {GENERATOR_PATH}")
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)


class OverrideContractTests(unittest.TestCase):
    def _with_overrides(self, body: str, monkeypatch: object = None) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            overrides = Path(temp_dir) / "konflux.rhoai.overrides"
            overrides.write_text(textwrap.dedent(body), encoding="utf-8")
            original = generator.OVERRIDES_FILE
            generator.OVERRIDES_FILE = overrides
            try:
                generator._validate_overrides()
            finally:
                generator.OVERRIDES_FILE = original

    def test_committed_overrides_validate(self):
        generator._validate_overrides()

    def test_override_without_rationale_comment_is_rejected(self):
        with self.assertRaisesRegex(generator.GenerationError, "rationale"):
            self._with_overrides(
                """
                huggingface-hub>=0.32.0

                # mentions nothing tracked
                pyjwt==2.13.0
                """
            )

    def test_undeclared_override_is_rejected(self):
        with self.assertRaisesRegex(generator.GenerationError, "exactly"):
            self._with_overrides(
                """
                # AIPCC-18230
                huggingface-hub>=0.32.0

                # 2.14.x
                pyjwt==2.13.0

                # no marker
                some-other-package==1.0.0
                """
            )


class SharedPyprojectContractTests(unittest.TestCase):
    def test_committed_shared_pyproject_is_odh_verbatim(self):
        generator._validate_shared_pyproject()

    def test_downstream_pyproject_edit_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            pyproject = Path(temp_dir) / "pyproject.toml"
            pyproject.write_text(
                textwrap.dedent(
                    """
                    [project]
                    dependencies = [
                        "huggingface-hub>=0.32.0",
                    ]
                    """
                ),
                encoding="utf-8",
            )
            original = generator.SHARED_PYPROJECT
            generator.SHARED_PYPROJECT = pyproject
            try:
                with self.assertRaisesRegex(
                    generator.GenerationError, "ODH-verbatim"
                ):
                    generator._validate_shared_pyproject()
            finally:
                generator.SHARED_PYPROJECT = original


class WorkflowContractTests(unittest.TestCase):
    def test_workflow_exists_and_watches_shared_pyproject(self):
        workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
        self.assertIn("  workflow_dispatch:", workflow)
        for watched in (
            "python/storage/pyproject.toml",
            "python/storage/konflux.rhoai.overrides",
            "python/storage/konflux-hdfs.in",
            "hack/rhoai/generate_storage.py",
        ):
            self.assertIn(watched, workflow)


if __name__ == "__main__":
    unittest.main()
