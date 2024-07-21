FROM docker.io/library/golang:1.22 as builder

WORKDIR /workspace

COPY passt/_kubevirt/cmd/cniplugins/passt-binding/cmd/ cmd/cniplugins/passt-binding/cmd/
COPY passt/_kubevirt/cmd/cniplugins/passt-binding/pkg/ cmd/cniplugins/passt-binding/pkg/
COPY passt/_kubevirt/pkg/util/ /workspace/pkg/util/
COPY passt/_kubevirt/staging/ staging/

COPY passt/_kubevirt/go.mod go.mod
COPY passt/_kubevirt/go.sum go.sum
RUN go mod edit -replace=kubevirt.io/kubevirt/cmd/cniplugins/passt-binding/pkg/plugin=/workspace/cmd/cniplugins/passt-binding/pkg/plugin
RUN go mod edit -replace=kubevirt.io/kubevirt/pkg/util/sysctl=/workspace/pkg/util/sysctl
RUN go mod edit -replace=kubevirt.io/kubevirt/pkg/util=/workspace/pkg/util
RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o network-passt-binding cmd/cniplugins/passt-binding/cmd/cni.go

FROM registry.access.redhat.com/ubi8/ubi-minimal
WORKDIR /
RUN mkdir /cni
COPY --from=builder /workspace/network-passt-binding /cni/network-passt-binding
