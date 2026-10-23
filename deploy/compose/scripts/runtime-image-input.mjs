const authorityPattern = /^(127\.0\.0\.1|localhost):([1-9][0-9]{0,4})$/;
const componentPattern = /^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$/;
const digestPattern = /^sha256:[a-f0-9]{64}$/;
const tagPattern = /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/;

export function localRepository(value) {
  if (typeof value !== 'string') throw new Error('local repository required');
  const slash = value.indexOf('/');
  if (slash < 0) throw new Error('local repository required');
  const authority = value.slice(0, slash);
  const match = authorityPattern.exec(authority);
  if (
    !match ||
    Number(match[2]) > 65535 ||
    !value
      .slice(slash + 1)
      .split('/')
      .every((part) => componentPattern.test(part))
  ) {
    throw new Error('canonical local repository required');
  }
  return { authority, repository: value.slice(slash + 1) };
}

export function imageTag(value) {
  const colon = typeof value === 'string' ? value.lastIndexOf(':') : -1;
  if (colon < 0) throw new Error('canonical local image tag required');
  const fullRepository = value.slice(0, colon);
  const parsed = localRepository(fullRepository);
  if (!tagPattern.test(value.slice(colon + 1)))
    throw new Error('canonical local image tag required');
  return { ...parsed, fullRepository, tag: value.slice(colon + 1) };
}

export function repositoryDigest(value, approvedRepository) {
  localRepository(approvedRepository);
  const prefix = `${approvedRepository}@`;
  if (
    typeof value !== 'string' ||
    !value.startsWith(prefix) ||
    !digestPattern.test(value.slice(prefix.length))
  ) {
    throw new Error('image must name approved repository by canonical digest');
  }
  return value.slice(prefix.length);
}

export function selectRepoDigest(refs, approvedRepository) {
  localRepository(approvedRepository);
  if (!Array.isArray(refs)) throw new Error('missing RepoDigests array');
  const matches = new Set();
  for (const ref of refs) {
    if (typeof ref !== 'string' || !ref.startsWith(`${approvedRepository}@`))
      continue;
    repositoryDigest(ref, approvedRepository);
    matches.add(ref);
  }
  if (matches.size !== 1)
    throw new Error('missing or ambiguous immutable pushed image digest');
  return [...matches][0];
}

export function buildArgs(args) {
  const options = new Map();
  for (let i = 0; i < args.length; i += 2) {
    const option = args[i],
      value = args[i + 1];
    if (
      !['--metadata-out', '--image-tag'].includes(option) ||
      options.has(option) ||
      !value ||
      value.startsWith('--')
    ) {
      throw new Error(
        'usage: build-real-dsh.mjs [--metadata-out <file>] [--image-tag <local repository:tag>]',
      );
    }
    options.set(option, value);
  }
  const tag = options.get('--image-tag') || '127.0.0.1:5000/auriga/real-dsh:w3';
  return { metadataOut: options.get('--metadata-out'), image: imageTag(tag) };
}

export function collectorArgs(args) {
  if (
    args.length < 2 ||
    args.length > 4 ||
    args[0]?.startsWith('--') ||
    args[1]?.startsWith('--')
  ) {
    throw new Error(
      'usage: collect-runtime-proofs.mjs <repo@digest> <temporary proof root> [--repository <local repository>]',
    );
  }
  if (
    args.length !== 2 &&
    (args.length !== 4 ||
      args[2] !== '--repository' ||
      !args[3] ||
      args[3].startsWith('--'))
  ) {
    throw new Error(
      'usage: collect-runtime-proofs.mjs <repo@digest> <temporary proof root> [--repository <local repository>]',
    );
  }
  const repository = args[3];
  if (repository) {
    localRepository(repository);
    repositoryDigest(args[0], repository);
  }
  return { image: args[0], output: args[1], repository };
}
