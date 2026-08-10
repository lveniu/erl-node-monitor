# HolmesGPT 0.38.1 native CentOS 7 runtime

The Linux service runs the pinned upstream commit
`7af34f5e716e28adcbcbd584cd4708434929f183` without Docker. The published
source archive contains the small downstream patch recorded in
`holmes-0.38.1-centos7.patch`. Native build 2 adds the disabled-OAuth import
guard and installs beside build 1 before atomically updating the runtime symlink.

The patch limits startup-time toolsets to `core_investigation`, `skills`, and
`prometheus/metrics`; disables MCP OAuth and the Robusta platform MCP surface;
short-circuits disabled OAuth tool substitution before importing MCP code; and
lazy-loads Azure/Kubernetes discovery code. This removes unused connector
dependencies whose current wheels require glibc newer than CentOS 7's 2.17.
It does not add Bash, arbitrary HTTP, Kubernetes, database, Kafka, or repair
tools.

The native requirements retain the upstream locked versions where compatible.
`jq` is pinned to `1.10.0` and `tiktoken` to `0.11.0`, the newest releases in
their compatible ranges that provide CPython 3.11 manylinux2014 wheels.
