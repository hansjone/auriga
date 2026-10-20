# Auriga NAS isolated test pack

Public deployment templates for a separate NAS test stack (`auriga-nas-test`).

- Operator guide: [`docs/dev/nas.md`](../../docs/dev/nas.md)
- Do not put real domains, LAN IPs, or credentials in this tree
- Build images via `.github/workflows/nas-bundle.yml` (`workflow_dispatch` + exact commit)
- Never deploy from a worker session; main controller runs site instantiation from private notes
