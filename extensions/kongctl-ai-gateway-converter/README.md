# kongctl AI Gateway converter extension

This directory contains a kongctl extension that exposes a kongctl-friendly
conversion interface for AI Gateway migration.

Build the extension runtime before linking it. kongctl extensions run an
existing executable; they do not compile source during install or link.

```sh
make build-extension
kongctl link extension ./extensions/kongctl-ai-gateway-converter
```

Convert a Kong Gateway 3.x decK file into kongctl AI Gateway declarative YAML:

```sh
kongctl convert ai-gateway deck.yaml \
  --from deck \
  --to kongctl \
  --gateway-name support-ai \
  --output-file aigw.yaml
```

Convert a kongctl AI Gateway declarative file back into Kong Gateway decK YAML:

```sh
kongctl convert ai-gateway aigw.yaml \
  --from kongctl \
  --to deck \
  --gateway-name support-ai \
  --output-file deck.yaml
```

The extension expects a kongctl AI Gateway file shape for `--from kongctl` and
emits that same shape for `--to kongctl`. The converter-native top-level
`models`, `providers`, and related collections remain an implementation detail.

For release delivery, stage archives with `kongctl-extension.yaml` at the
archive root:

```text
kongctl-extension.yaml
README.md
bin/kongctl-ext-ai-gateway-converter
```
