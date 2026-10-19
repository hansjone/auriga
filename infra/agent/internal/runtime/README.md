# runtime

Instance drivers: `Driver` interface, in-memory `Fake` (tests), and `Docker` (Engine API / Podman).

`Preparing` wraps a driver to fetch artifacts, ensure instance directories, and render config before `Ensure`.
