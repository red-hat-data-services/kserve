ARG PYTHON_VERSION=3.9
ARG BASE_IMAGE=registry.access.redhat.com/ubi8/ubi
ARG VENV_PATH=/prod_venv

FROM ${BASE_IMAGE} as builder

# Install Poetry
ARG POETRY_HOME=/opt/poetry
ARG POETRY_VERSION=1.4.0

# Required for building packages for arm64 arch
RUN yum -y update && yum -y install python39 python39-devel gcc

RUN python3 -m venv ${POETRY_HOME} && ${POETRY_HOME}/bin/pip install poetry==${POETRY_VERSION}
ENV PATH="$PATH:${POETRY_HOME}/bin"

# Activate virtual env
ARG VENV_PATH
ENV VIRTUAL_ENV=${VENV_PATH}
RUN python3 -m venv $VIRTUAL_ENV
ENV PATH="$VIRTUAL_ENV/bin:$PATH"

# Addressing vulnerability scans by upgrading pip/setuptools
RUN python3 -m pip install --upgrade pip setuptools==69.5.1

COPY kserve/pyproject.toml kserve/poetry.lock kserve/
RUN cd kserve && poetry install --no-root --no-interaction --no-cache --extras "storage"
COPY kserve kserve
RUN cd kserve && poetry install --no-interaction --no-cache --extras "storage"

RUN yum -y update && yum install -y \
    gcc \
    krb5-devel \
    && rm -rf /var/lib/apt/lists/*

# Fixes CVE-2024-24762 - Regular Expression Denial of Service (ReDoS)
# Remove the fastapi when this is addressed:  https://issues.redhat.com/browse/RHOAIENG-3894
# or ray releses a new version that removes the fastapi version pinning and it gets updated on KServe
RUN pip install --no-cache-dir krbcontext==0.10 hdfs~=2.6.0 requests-kerberos==0.14.0
# address CVE-2024-47874
RUN pip install --no-cache-dir starlette==0.40.0


FROM registry.access.redhat.com/ubi8/ubi-minimal as prod

COPY third_party third_party

# Activate virtual env
ARG VENV_PATH
ENV VIRTUAL_ENV=${VENV_PATH}
ENV PATH="$VIRTUAL_ENV/bin:$PATH"

RUN microdnf install python39 shadow-utils && microdnf clean all
RUN adduser kserve -m -u 1000 -d /home/kserve

COPY --from=builder --chown=kserve:kserve $VIRTUAL_ENV $VIRTUAL_ENV
COPY --from=builder kserve kserve
COPY ./storage-initializer /storage-initializer

RUN chmod +x /storage-initializer/scripts/initializer-entrypoint
RUN mkdir /work
WORKDIR /work

USER 1000
ENTRYPOINT ["/storage-initializer/scripts/initializer-entrypoint"]
