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

- Multi-stage build: Go binary compiled in `golang:1.24.1-bookworm`, then copied into a slim `ubuntu:24.04` runtime with only the OpenVINO GenAI runtime and Intel GPU/NPU userspace libraries.
- OCI labels: `org.opencontainers.image.{source,url,vendor,description}`.
- Runs as non-root user `ollama` (uid 1000).
