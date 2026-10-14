# Contributing to data-product-controller

You can help by improving the documentation, fixing an example, or changing the controller, the
registry or the chart. Open or find an
[issue](https://github.com/devantler-tech/kweft/issues) before you start, and
reference it in the pull request; only a small, obvious fix such as a typo or a broken link may go
straight to a pull request. The
[roadmap](https://github.com/devantler-tech/kweft/issues/1) shows what is
planned. Report sensitive vulnerabilities through this repository's private
[Report a vulnerability](https://github.com/devantler-tech/kweft/security/advisories/new)
form, not a public issue or pull request.

## Make a first documentation change

You need Git and a GitHub account that can push to your own fork. This path needs no Go toolchain,
no cluster and no credentials for any product or cloud service.

Fork `devantler-tech/kweft` on GitHub. Replace `YOUR-LOGIN` with your login:

```sh
git clone https://github.com/YOUR-LOGIN/data-product-controller.git
cd data-product-controller
git switch -c docs/first-contribution
```

Make a small, obvious correction in `README.md`, such as a typo, then check the change for whitespace errors:

```sh
git diff --check
```

The command must exit successfully and print nothing. Commit, push the branch to your fork and
open a **draft** pull request with a [Conventional Commits](https://www.conventionalcommits.org/)
title such as `docs: fix a typo in the local example`. Mark it ready for review once its checks
have passed and you have answered the review comments.

On the pull request, the organization's lint check also spell-checks the documentation; add a
correct word it does not know to [`cspell.json`](cspell.json). This repository's own
[CI workflow](.github/workflows/ci.yaml) runs its complete set of jobs for a documentation change,
including the cluster-backed ones, so expect the checks to take a while. The
[CI coverage note](docs/continuous-integration.md) explains which changes run a shorter set.

## Change the code

You also need Go at the version in [`go.mod`](go.mod) or newer. The
[README](README.md#try-it-locally) shows how to run the example product, which is the quickest way
to see the code working.

Run the complete validation list in [AGENTS.md](AGENTS.md#validation) before you open a pull
request, in the order given there. Its first Go checks need no cluster and no credentials:

```sh
go build ./...
go test ./...
```

Both exit successfully when the code compiles and the unit tests pass; the first run downloads
dependencies and can take a few minutes.

The other entries in that list need more tools:

| Check                                                                             | Needs                                                                                 |
|-----------------------------------------------------------------------------------|---------------------------------------------------------------------------------------|
| `go test -tags=browser ./internal/browser`                                        | A Chromium-based browser; the test library downloads one when it finds none installed |
| The `sh scripts/*.test.sh` entries and `helm lint charts/kweft` | Helm and yq                                                                           |
| `golangci-lint fmt` and `golangci-lint run`                                       | golangci-lint                                                                         |

The only checks you may leave to CI are the cluster-backed ones in the next section. CI runs the
validation list on the pull request, except
that a change limited to the reviewed browser test files runs the shorter set described in the
[CI coverage note](docs/continuous-integration.md).

### Checks that hosted CI runs for you

Some required checks start real, disposable Kubernetes clusters and databases. They need Docker
and several gigabytes of free disk space, and they are slow, so you are not expected to run them
locally. CI runs them on the pull request, and that hosted result is the acceptance evidence:

- the source-integration suites: `bash tests/source/run.sh`, described in the
  [source integration guide](docs/source-integration-tests.md);
- the Document, Graph and PostgreSQL acceptance jobs under `tests/provider/`, described in the
  [Document](docs/real-document-acceptance.md), [Graph](docs/real-graph-acceptance.md) and
  [PostgreSQL](docs/real-postgresql-acceptance.md) acceptance guides;
- the PostgreSQL AGE image job, described in the
  [image guide](docs/postgresql-age-image.md).

If you do run one locally, use a machine where creating and deleting local clusters is safe. None
of these checks, and nothing in this guide, touches a shared or production cluster: deployment
belongs to the platform that installs the released chart.

### Rules that keep changes reviewable

- Write a failing test first for a change in behavior, then the smallest change that passes it.
- Put new behavior behind a feature flag that is off by default, and test both states.
- Keep product data, passwords, tokens and connection strings out of the `DataProduct` resource and
  its status. It carries descriptions only.
- Do not describe roadmap work as delivered. Documentation states what the code does today.
- Update the documentation a change affects in the same pull request. Record a design decision as
  a new file in [`docs/adr/`](docs/adr/); existing decision records stay as written.

### Generated files

Never edit these by hand:

- `api/v1alpha1/zz_generated.deepcopy.go`;
- `config/crd/bases/` and `config/rbac/`;
- the two copies of the generated resource definition,
  `charts/kweft/crds/data.devantler.tech_dataproducts.yaml` and
  `deploy/data.devantler.tech_dataproducts.yaml`.

After editing the API types in `api/v1alpha1/`, or any `+kubebuilder` marker anywhere in the code
(the access markers live in `main.go` and `internal/controller/dataproduct_controller.go`),
regenerate them and copy the resource definition to both places:

```sh
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 \
  object rbac:roleName=manager-role crd paths=./... \
  output:crd:artifacts:config=config/crd/bases \
  output:rbac:artifacts:config=config/rbac
cp config/crd/bases/data.devantler.tech_dataproducts.yaml \
  charts/kweft/crds/data.devantler.tech_dataproducts.yaml
cp config/crd/bases/data.devantler.tech_dataproducts.yaml deploy/data.devantler.tech_dataproducts.yaml
```

Commit the regenerated files with the change. The three copies of the resource definition must be
identical; `cmp` on any two of them prints nothing when they are.

## What happens to your pull request

A maintainer reviews it, and the required checks must pass before it merges. Changes from a fork
are reviewed before any of their code is run outside CI. Keep a pull request to one concern so it
can be reviewed and reverted on its own.

AI coding agents working in this repository follow [AGENTS.md](AGENTS.md), which also holds the
detailed architecture boundaries for each part of the code.
