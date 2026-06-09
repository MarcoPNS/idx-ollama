# idx-ollama-openvino

Optimized [Ollama (OpenVINO fork)](https://github.com/zhaohb/ollama_openvino) image for **UGREEN iDX NAS** systems with an **Intel Ultra** chip — uses OpenVINO + the Intel NPU/GPU driver stack to accelerate inference.

The image is published to Docker Hub: **[marcospace/idx-ollama-openvino](https://hub.docker.com/r/marcospace/idx-ollama-openvino)**

## Quick start

```bash
docker compose up -d
```

The bundled `docker-compose.yaml` already pulls `marcospace/idx-ollama-openvino:latest` and exposes:

- Ollama API → `http://<nas-ip>:11434`
- Open WebUI  → `http://<nas-ip>:3001`
- **Importer** → `http://<nas-ip>:3161` (upload OpenVINO-IR models from the browser)

## Importing an OpenVINO model

Open `http://<nas-ip>:3161` in a browser:

1. (Optional) Enter a model name, e.g. `DeepSeek-R1-Distill-Qwen-7B-int4-ov:v1`. If empty, the importer uses the first `# model: name:tag` line of the Modelfile, or falls back to the tar.gz filename.
2. Pick the model `*.tar.gz` (and any sibling files referenced by `FROM`).
3. Paste the Modelfile contents (or upload it as a file). Example:

   ```
   FROM DeepSeek-R1-Distill-Qwen-7B-int4-ov.tar.gz
   ModelType "OpenVINO"
   InferDevice "GPU"
   PARAMETER repeat_penalty 1.0
   PARAMETER top_p 1.0
   PARAMETER temperature 1.0
   ```

4. Click **Import model**. The importer stages the tar.gz into a shared volume visible to the Ollama container, rewrites the `FROM` line, and calls Ollama's `POST /api/create`. On success the model is immediately available in Open WebUI.

## Hardware requirements

- Intel Core Ultra / Meteor Lake or later (CPU, iGPU, and NPU)
- Linux kernel ≥ 6.6 with `accel` and `dri` nodes present
- For the NPU: `intel_vpu` module loaded; `/dev/accel/accel0` visible to the container

## Required host groups (auto-added by `docker-compose.yaml`)

| GID | Name        | Purpose                       |
| --- | ----------- | ----------------------------- |
| 44  | video       | /dev/dri/renderD*             |
| 105 | render      | /dev/dri/renderD* (alt)       |
| 261 | accel       | /dev/accel/* (NPU)            |
| 226 | systemd-journal | some distro permissions   |

Verify with `getent group 44 105 226 261` on the host.


## Image details

- **Ollama image** — multi-stage build: Go binary compiled in `golang:1.24.1-bookworm`, then copied into a slim `ubuntu:24.04` runtime with only the OpenVINO GenAI runtime and Intel GPU/NPU userspace libraries. Runs as non-root user `ollama` (uid 1000).
- **Importer image** — distroless `gcr.io/distroless/static-debian12:nonroot` with a tiny Go HTTP server (no Node, no Python). Talks to Ollama over HTTP. Shares a `ollama_imports` volume with the Ollama container so the importer can drop tar.gz files where the Ollama container can read them.
- Both images carry OCI labels (`org.opencontainers.image.{source,url,vendor,description}`).
