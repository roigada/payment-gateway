# Mock Bank Provenance

This directory bundles the Mock Bank as third-party demo infrastructure for the root Payment
Gateway demo. It is not part of the Payment Gateway implementation, which is authored under
[`gateway/`](../gateway/README.md).

## Source

Upstream: <https://github.com/benx421/payment-gateway>

The code in this directory is copied dependency code and should stay easy to compare with its
upstream source. It is vendored rather than referenced so that `make up` from the repository
root brings the whole environment up in one command.

## Licensing

**The upstream repository is published without a license**, as is the
[Backend Engineer Path](https://github.com/benx421/backend-engineer-path) curriculum it belongs
to. Under default copyright, that means this code is all rights reserved by its author. It is
reproduced here with attribution, for demonstration purposes.

The repository's [LICENSE](../LICENSE) covers the Payment Gateway only. It does not extend to
this directory, and nothing here is offered under its terms.

If you are the upstream author and would prefer this copy be removed or replaced with a setup
step, please open an issue.
