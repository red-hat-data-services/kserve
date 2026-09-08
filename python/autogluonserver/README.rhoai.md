# RHOAI AutoGluon packaging

The public `pyproject.toml` and `uv.lock` are synchronized from ODH. RHOAI
package policy is kept separately in `rhoai-overrides.toml` and rendered into:

- `pyproject.rhoai.toml`;
- `uv.rhoai.lock`;
- `autogluon-all-requirements.txt`.

Generate the three content artifacts locally with the repository-pinned uv
version (`0.7.8`):

```bash
uv run hack/rhoai/generate_autogluon.py \
  --base python/autogluonserver/pyproject.toml \
  --overlay python/autogluonserver/rhoai-overrides.toml \
  --output-dir python/autogluonserver
```

Use `--check` in validation jobs. The downstream update workflow regenerates
and commits the same three files after relevant changes land on `main`. Do not
edit any generated file by hand.
