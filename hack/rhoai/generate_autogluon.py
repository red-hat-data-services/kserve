# /// script
# requires-python = ">=3.11"
# dependencies = ["tomlkit==0.13.2"]
# ///

"""Generate the downstream RHOAI AutoGluon dependency artifacts."""

from __future__ import annotations

import argparse
import copy
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from typing import Any
from urllib.parse import urlsplit

import tomlkit


UV_VERSION = "0.7.8"
CONTENT_OUTPUTS = (
    "pyproject.rhoai.toml",
    "uv.rhoai.lock",
    "autogluon-all-requirements.txt",
)


class GenerationError(RuntimeError):
    """An invalid input or generated artifact."""


def _table(value: Any, name: str) -> Any:
    if not hasattr(value, "keys"):
        raise GenerationError(f"{name} must be a TOML table")
    return value


def _exact_keys(value: Any, expected: set[str], name: str) -> None:
    actual = set(_table(value, name).keys())
    if actual != expected:
        raise GenerationError(
            f"{name} keys must be {sorted(expected)}; got {sorted(actual)}"
        )


def _render_project(base_path: Path, overlay_path: Path) -> tuple[str, str]:
    base = tomlkit.parse(base_path.read_text(encoding="utf-8"))
    overlay = tomlkit.parse(overlay_path.read_text(encoding="utf-8"))

    _exact_keys(
        overlay,
        {"project", "build-system", "dependency-groups", "tool"},
        "overlay",
    )
    _exact_keys(overlay["project"], {"requires-python", "dependencies"}, "project")
    _exact_keys(overlay["build-system"], {"requires", "build-backend"}, "build-system")
    _exact_keys(overlay["dependency-groups"], {"rhoai-build"}, "dependency-groups")
    _exact_keys(overlay["tool"], {"uv"}, "tool")
    _exact_keys(overlay["tool"]["uv"], {"index", "sources"}, "tool.uv")

    dependencies = overlay["project"]["dependencies"]
    if not dependencies or not all(isinstance(item, str) for item in dependencies):
        raise GenerationError("project.dependencies must be a non-empty string array")
    required_packages = ("autogluon.tabular", "autogluon.timeseries")
    if not all(
        any(dep.startswith(package) for dep in dependencies)
        for package in required_packages
    ):
        raise GenerationError("both patched AutoGluon packages are required")

    indexes = overlay["tool"]["uv"]["index"]
    if len(indexes) != 1 or indexes[0].get("explicit") is not True:
        raise GenerationError("the overlay must define one explicit RHOAI index")
    index_name = str(indexes[0].get("name", ""))
    index_url = str(indexes[0].get("url", ""))
    parsed_url = urlsplit(index_url)
    if parsed_url.scheme != "https" or not parsed_url.netloc:
        raise GenerationError("the RHOAI index must use an absolute HTTPS URL")
    if parsed_url.username or parsed_url.password:
        raise GenerationError("the RHOAI index URL must not contain credentials")

    sources = _table(overlay["tool"]["uv"]["sources"], "tool.uv.sources")
    expected_sources = {
        "autogluon.common",
        "autogluon.core",
        "autogluon.features",
        "autogluon.tabular",
        "autogluon.timeseries",
    }
    _exact_keys(sources, expected_sources, "tool.uv.sources")
    for package, source in sources.items():
        if set(_table(source, f"tool.uv.sources.{package}").keys()) != {"index"}:
            raise GenerationError(f"{package} must declare only an index source")
        if source["index"] != index_name:
            raise GenerationError(f"{package} must use the RHOAI index")

    base["project"]["requires-python"] = copy.deepcopy(
        overlay["project"]["requires-python"]
    )
    base["project"]["dependencies"] = copy.deepcopy(dependencies)
    base["build-system"] = copy.deepcopy(overlay["build-system"])
    base["dependency-groups"]["rhoai-build"] = copy.deepcopy(
        overlay["dependency-groups"]["rhoai-build"]
    )
    for package, source in sources.items():
        base["tool"]["uv"]["sources"][package] = copy.deepcopy(source)
    base["tool"]["uv"]["index"] = copy.deepcopy(indexes)
    return tomlkit.dumps(base), index_url


def _run_uv(project_dir: Path) -> tuple[bytes, bytes]:
    version = subprocess.run(
        ["uv", "--version"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    if version != f"uv {UV_VERSION}":
        raise GenerationError(f"expected uv {UV_VERSION}, got {version}")

    environment = os.environ.copy()
    environment["UV_NO_PROGRESS"] = "1"
    subprocess.run(["uv", "lock"], cwd=project_dir, check=True, env=environment)
    subprocess.run(
        ["uv", "lock", "--check"], cwd=project_dir, check=True, env=environment
    )
    requirements_body = project_dir / "requirements.body.txt"
    subprocess.run(
        [
            "uv",
            "export",
            "--locked",
            "--format",
            "requirements.txt",
            "--no-dev",
            "--group",
            "rhoai-build",
            "--no-header",
            "--no-emit-project",
            "--no-emit-package",
            "kserve",
            "--no-emit-package",
            "kserve-storage",
            "--output-file",
            str(requirements_body),
        ],
        cwd=project_dir,
        check=True,
        env=environment,
        stdout=subprocess.DEVNULL,
    )
    return (project_dir / "uv.lock").read_bytes(), requirements_body.read_bytes()


def _generate(
    base_path: Path,
    overlay_path: Path,
    output_dir: Path,
) -> dict[str, bytes]:
    rendered_text, index_url = _render_project(base_path, overlay_path)
    rendered = rendered_text.encode()
    python_dir = base_path.parent.parent
    for local_project in ("kserve", "storage"):
        if not (python_dir / local_project / "pyproject.toml").is_file():
            raise GenerationError(f"missing local project: {local_project}")

    with tempfile.TemporaryDirectory(prefix="autogluon-rhoai-") as temp_dir:
        temp_python = Path(temp_dir) / "python"
        temp_python.mkdir()
        ignored = shutil.ignore_patterns(".venv", "__pycache__", *CONTENT_OUTPUTS)
        for project in ("kserve", "storage"):
            shutil.copytree(python_dir / project, temp_python / project, ignore=ignored)
        temp_project = temp_python / "autogluonserver"
        shutil.copytree(base_path.parent, temp_project, ignore=ignored)
        (temp_project / "pyproject.toml").write_bytes(rendered)
        existing_lock = output_dir / "uv.rhoai.lock"
        if existing_lock.is_file():
            shutil.copy2(existing_lock, temp_project / "uv.lock")
        lock, requirements_body = _run_uv(temp_project)

    if b"--index-url" in requirements_body or b"--extra-index-url" in requirements_body:
        raise GenerationError("uv export unexpectedly emitted an index directive")
    if not requirements_body.endswith(b"\n"):
        requirements_body += b"\n"
    requirements = f"--index-url {index_url}\n\n".encode() + requirements_body

    outputs = {
        "pyproject.rhoai.toml": rendered,
        "uv.rhoai.lock": lock,
        "autogluon-all-requirements.txt": requirements,
    }
    return outputs


def _apply_outputs(outputs: dict[str, bytes], output_dir: Path, check: bool) -> int:
    changed = [
        name
        for name, content in outputs.items()
        if not (output_dir / name).is_file()
        or (output_dir / name).read_bytes() != content
    ]
    if check:
        if changed:
            print(
                "generated AutoGluon artifacts differ: " + ", ".join(changed),
                file=sys.stderr,
            )
            return 1
        print("generated AutoGluon artifacts are current")
        return 0

    output_dir.mkdir(parents=True, exist_ok=True)
    for name, content in outputs.items():
        destination = output_dir / name
        with tempfile.NamedTemporaryFile(dir=output_dir, delete=False) as temporary:
            temporary.write(content)
            temporary_path = Path(temporary.name)
        temporary_path.replace(destination)
    print("updated AutoGluon artifacts: " + ", ".join(outputs))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", type=Path, required=True)
    parser.add_argument("--overlay", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--check", action="store_true")
    arguments = parser.parse_args()

    try:
        outputs = _generate(
            arguments.base.resolve(),
            arguments.overlay.resolve(),
            arguments.output_dir.resolve(),
        )
        return _apply_outputs(outputs, arguments.output_dir.resolve(), arguments.check)
    except (GenerationError, tomlkit.exceptions.ParseError) as error:
        parser.error(str(error))
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
