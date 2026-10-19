# Contributing

Thanks for your interest. The project is in early development; large changes should start as an issue.

## Ground rules

- **Contracts first.** Interfaces between components live in [`contracts/`](contracts/README.md).
  Change the contract in the same pull request as the code, and keep changes backward compatible
  within a major version (CI runs `buf breaking` and an OpenAPI diff).
- **Each Java service may depend only on other services' `api` modules**, never on their `impl`.
  Architecture tests enforce this.
- **No internal information.** Never commit real hostnames, IP addresses, accounts, keys or company
  names. Use placeholders such as `example.com`, `192.0.2.10` and `alice`. CI runs gitleaks.
- Tests are required for new behavior; the full test suite must pass.

## Contributor License Agreement

Auriga is dual-licensed (AGPL-3.0 and commercial, see [LICENSING.md](LICENSING.md)), so every
external contributor must sign the CLA in [`CLA.md`](CLA.md) once. The CLA check on your first pull
request tells you how to sign.
