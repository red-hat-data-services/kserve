# RHOAI AutoGluon packaging

The public `pyproject.toml` and `uv.lock` are synchronized from ODH. RHOAI
package policy is kept separately in `rhoai-overrides.toml` and rendered into:

- `pyproject.rhoai.toml`;
- `uv.rhoai.lock`;
- `autogluon-all-requirements.txt`; and
- `rhoai-generation.toml` after a relevant change lands on downstream `main`.

Generate the three content artifacts locally with the repository-pinned uv
version (`0.7.8`):

```bash
uv run hack/rhoai/generate_autogluon.py \
  --base python/autogluonserver/pyproject.toml \
  --overlay python/autogluonserver/rhoai-overrides.toml \
  --output-dir python/autogluonserver
```

Use `--check` in validation jobs. The downstream update workflow passes
`--source-commit` and commits the resulting provenance file. Provenance is an
audit record; release builds continue to use their existing push triggers. Do
not edit any generated file by hand.
