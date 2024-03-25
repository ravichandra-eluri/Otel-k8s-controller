IMG ?= ghcr.io/ravichandra-eluri/otel-k8s-controller:latest

.PHONY: all build test lint docker-build docker-push deploy undeploy

all: build

## Build the manager binary
build:
	go build -o bin/manager ./cmd/manager

## Run unit tests
test:
	go test ./... -v -coverprofile cover.out

## Run linter
lint:
	golangci-lint run ./...

## Build Docker image
docker-build:
	docker build -t $(IMG) .

## Push Docker image
docker-push:
	docker push $(IMG)

## Generate CRD manifests (requires controller-gen)
manifests:
	controller-gen crd rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd

## Generate DeepCopy methods
generate:
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

## Install CRDs into the cluster
install:
	kubectl apply -f config/crd/

## Deploy controller to the cluster
deploy:
	kubectl apply -f config/rbac/
	kubectl apply -f config/manager/

## Remove controller from the cluster
undeploy:
	kubectl delete -f config/manager/ --ignore-not-found
	kubectl delete -f config/rbac/ --ignore-not-found
	kubectl delete -f config/crd/ --ignore-not-found

## Apply sample CR
sample:
	kubectl apply -f config/samples/otelcollector_v1alpha1.yaml

## Run locally against current kubeconfig cluster
run:
	go run ./cmd/manager/main.go
