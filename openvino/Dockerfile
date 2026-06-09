# syntax=docker/dockerfile:1.7
# ---------- Build stage ----------
FROM golang:1.24.1-bookworm AS builder

ARG GENAI_NIGHTLY=2026.3.0.0.dev20260516
ARG GENAI_PKG=openvino_genai_ubuntu24_${GENAI_NIGHTLY}_x86_64
ARG GENAI_DIR=/opt/openvino_genai

WORKDIR /src

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        wget \
    && rm -rf /var/lib/apt/lists/*

RUN wget -q https://storage.openvinotoolkit.org/repositories/openvino_genai/packages/nightly/${GENAI_NIGHTLY}/${GENAI_PKG}.tar.gz \
    && tar -xzf ${GENAI_PKG}.tar.gz \
    && rm ${GENAI_PKG}.tar.gz

ENV GENAI_DIR=${GENAI_DIR}
RUN mv ${GENAI_PKG} ${GENAI_DIR}

ENV CGO_ENABLED=1
ENV GODEBUG=cgocheck=0
ENV CGO_CFLAGS=-I${GENAI_DIR}/runtime/include
ENV CGO_LDFLAGS=-L${GENAI_DIR}/runtime/lib/intel64
ENV LD_LIBRARY_PATH=${GENAI_DIR}/runtime/lib/intel64
ENV PATH=${GENAI_DIR}/runtime/bin/intel64:${PATH}

RUN git clone --depth=1 https://github.com/zhaohb/ollama_openvino.git /src/ollama_openvino
WORKDIR /src/ollama_openvino
RUN go build -trimpath -ldflags="-s -w" -o /out/ollama .

# ---------- Runtime stage ----------
FROM ubuntu:24.04 AS runtime

ARG GENAI_NIGHTLY=2026.3.0.0.dev20260516
ARG GENAI_PKG=openvino_genai_ubuntu24_${GENAI_NIGHTLY}_x86_64
ENV GENAI_DIR=/opt/openvino_genai

LABEL org.opencontainers.image.title="idx-ollama-openvino" \
      org.opencontainers.image.description="Ollama (OpenVINO fork) optimized for Intel Ultra / NPU on UGREEN iDX NAS" \
      org.opencontainers.image.source="https://github.com/MarcoPNS/idx-ollama" \
      org.opencontainers.image.url="https://hub.docker.com/r/marcospace/idx-ollama-openvino" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="marcospace"

SHELL ["/bin/bash", "-c"]

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        libtbb12 \
        wget \
    && wget -qO- https://repositories.intel.com/gpu/intel-graphics.key | gpg --yes --dearmor --output /usr/share/keyrings/intel-graphics.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/intel-graphics.gpg] https://repositories.intel.com/gpu/ubuntu noble client" > /etc/apt/sources.list.d/intel-gpu.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        clinfo \
        hwinfo \
        intel-igc-cm \
        intel-level-zero-gpu \
        intel-media-va-driver-non-free \
        intel-opencl-icd \
        ocl-icd-opencl-dev \
        level-zero \
        level-zero-dev \
        libgbm1 \
        libgl1-mesa-dri \
        libglapi-mesa \
        libigc-dev \
        libigdfcl-dev \
        libigdgmm12 \
        libmfx1 \
        libmfxgen1 \
        libvpl2 \
        libxatracker2 \
        mesa-va-drivers \
        mesa-vdpau-drivers \
        mesa-vulkan-drivers \
        udev \
        va-driver-all \
        libva2 \
        libvpl-tools \
        libmfx-dev \
        vainfo \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Intel NPU driver (kernel-mode userspace bits)
ARG NPU_DRIVER_VERSION=v1.26.0
ARG NPU_DRIVER_BUILD=20251125-19665715237
RUN wget -q https://github.com/intel/linux-npu-driver/releases/download/${NPU_DRIVER_VERSION}/linux-npu-driver-${NPU_DRIVER_VERSION}.${NPU_DRIVER_BUILD}-ubuntu2404.tar.gz -O /tmp/npu-driver.tar.gz \
    && mkdir -p /tmp/npu-driver \
    && tar -xf /tmp/npu-driver.tar.gz -C /tmp/npu-driver \
    && dpkg -i /tmp/npu-driver/*.deb || (apt-get -f install -y && dpkg -i /tmp/npu-driver/*.deb) \
    && rm -rf /tmp/npu-driver /tmp/npu-driver.tar.gz

# OpenVINO GenAI runtime (re-downloaded into runtime layer — version pinned to build arg)
RUN wget -q https://storage.openvinotoolkit.org/repositories/openvino_genai/packages/nightly/${GENAI_NIGHTLY}/${GENAI_PKG}.tar.gz -O /tmp/genai.tar.gz \
    && tar -xzf /tmp/genai.tar.gz \
    && mv ${GENAI_PKG} ${GENAI_DIR} \
    && rm /tmp/genai.tar.gz

ENV LD_LIBRARY_PATH=${GENAI_DIR}/runtime/lib/intel64
ENV PATH=${GENAI_DIR}/runtime/bin/intel64:${PATH}
ENV OLLAMA_HOST=0.0.0.0:11434

COPY --from=builder /out/ollama /usr/bin/ollama

RUN useradd -m -u 1000 -s /bin/bash ollama \
    && mkdir -p /home/ollama/.ollama \
    && chown -R ollama:ollama /home/ollama

USER ollama
WORKDIR /home/ollama

EXPOSE 11434

# Source OpenVINO env then start ollama
ENTRYPOINT ["/bin/bash", "-lc", "source ${GENAI_DIR}/setupvars.sh && exec /usr/bin/ollama serve"]
