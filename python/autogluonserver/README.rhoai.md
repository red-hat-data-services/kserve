# RHOAI AutoGluon packaging

The public `pyproject.toml` and `uv.lock` are synchronized from ODH. The
downstream-owned `pyproject.rhoai.toml` is the RHOAI packaging source of truth.
The generator uses it to produce:

- `uv.rhoai.lock`;
- `autogluon-all-requirements.txt`.

Generate the two content artifacts locally with the repository-pinned uv
version (`0.7.8`):

```bash
uv run hack/rhoai/generate_autogluon.py \
  --project python/autogluonserver/pyproject.rhoai.toml \
  --output-dir python/autogluonserver
```

Use `--check` to validate that the committed generated files are current. The
downstream update workflow regenerates and commits the same two files after
relevant changes land on `main`. Edit the RHOAI pyproject directly; do not
edit the generated lock or requirements file by hand.
