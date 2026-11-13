import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { requireValid } from './schema.mjs';
import {
  openSync,
  writeSync,
  fsyncSync,
  closeSync,
  readFileSync,
  unlinkSync,
  existsSync,
  readdirSync,
} from 'node:fs';
import { join } from 'node:path';
import { safePath } from './vault.mjs';
const fault = (code) => Object.assign(new Error(code), { code });
export class Streams {
  constructor(broker, directory) {
    this.directory = directory;
    this.broker = broker;
    this.streams = new Map();
    this.uploads = new Map();
    this.changed = () => {
      for (const stream of [...this.streams.values()])
        this.end(stream, 'closed');
      for (const upload of this.uploads.values()) this.removeUpload(upload);
      this.uploads.clear();
    };
    broker.on('changed', this.changed);
    this.timer = setInterval(() => {
      for (const stream of [...this.streams.values()])
        if (
          performance.now() - stream.touched >= 30000 ||
          stream.until <= performance.now()
        ) {
          if (stream.terminal) this.streams.delete(stream.id);
          else this.end(stream, 'error', 'common.unavailable');
        }
      for (const [id, upload] of this.uploads)
        if (upload.until <= performance.now()) {
          this.removeUpload(upload);
          this.uploads.delete(id);
        }
    }, 1000);
    this.timer.unref();
    if (directory)
      for (const name of readdirSync(directory))
        if (/^[0-9a-f-]{36}\.upload$/.test(name))
          unlinkSync(safePath(join(directory, name)));
  }
  create(context, owner, cancel = () => {}, upload = false) {
    if (this.streams.size >= 16) throw fault('common.rate_limited');
    const stream = {
      id: randomUUID(),
      context,
      owner,
      credit: 0,
      queued: [],
      queuedBytes: 0,
      sequence: 0,
      touched: performance.now(),
      until: performance.now() + (upload ? 300000 : 86400000),
      cancel,
      upload,
    };
    this.streams.set(stream.id, stream);
    return stream;
  }
  get(id, owner) {
    const stream = this.streams.get(id);
    if (!stream || stream.owner !== owner) throw fault('common.forbidden');
    this.broker.assert(stream.context);
    return stream;
  }
  event(stream, type, extra = {}) {
    const event = requireValid('ShellAuthStreamEvent', {
      streamId: stream.id,
      shellEpoch: stream.context.epoch,
      revision: stream.context.revision,
      sequence: stream.sequence++,
      type,
      ...extra,
    });
    return event;
  }
  deliver(stream, event, bytes = 0) {
    if (
      stream.listener &&
      !stream.queued.length &&
      (bytes === 0 || stream.credit >= bytes)
    ) {
      if (bytes) stream.credit -= bytes;
      stream.reply?.(event);
      stream.reply = undefined;
      stream.listener(event);
      return;
    }
    if (stream.queuedBytes + bytes > 1048576 || stream.queued.length >= 256) {
      this.end(stream, 'error', 'common.rate_limited');
      return;
    }
    stream.queued.push({ event, bytes });
    stream.queuedBytes += bytes;
  }
  push(stream, bytes) {
    if (!this.streams.has(stream.id)) return;
    this.broker.assert(stream.context);
    for (let offset = 0; offset < bytes.length; offset += 65536) {
      const chunk = bytes.subarray(offset, offset + 65536);
      this.deliver(
        stream,
        this.event(stream, 'data', { data: chunk.toString('base64') }),
        chunk.length,
      );
    }
  }
  flush(stream) {
    while (stream.listener && stream.queued.length) {
      const head = stream.queued[0];
      if (head.bytes > stream.credit) break;
      stream.queued.shift();
      stream.queuedBytes -= head.bytes;
      stream.credit -= head.bytes;
      stream.reply?.(head.event);
      stream.reply = undefined;
      stream.listener(head.event);
    }
    if (stream.terminal && !stream.queued.length && stream.listener) {
      stream.listener(stream.terminal);
      stream.terminal = undefined;
      this.streams.delete(stream.id);
    }
  }
  subscribe(command, owner, listener) {
    requireValid('ShellAuthStreamSubscription', command);
    const stream = this.get(command.streamId, owner);
    if (
      command.shellEpoch !== stream.context.epoch ||
      command.expectedRevision !== stream.context.revision ||
      stream.listener
    )
      throw fault('common.conflict');
    stream.listener = listener;
    stream.touched = performance.now();
    this.flush(stream);
    return () => this.end(stream, 'closed');
  }
  end(stream, type, code) {
    if (!this.streams.has(stream.id) || stream.terminal) return;
    stream.cancel();
    if (stream.upload && type !== 'end') {
      const upload = this.uploads.get(stream.uploadId);
      if (upload) this.removeUpload(upload);
      this.uploads.delete(stream.uploadId);
    }
    const event = this.event(
      stream,
      type,
      code
        ? { code, error: { code, retryable: code === 'common.unavailable' } }
        : {},
    );
    stream.reply?.(event);
    stream.reply = undefined;
    if (type === 'end' && stream.queued.length) {
      stream.terminal = event;
      return;
    }
    stream.queued = [];
    stream.queuedBytes = 0;
    if (stream.listener) {
      stream.listener(event);
      this.streams.delete(stream.id);
    } else {
      stream.terminal = event;
    }
    return event;
  }
  command(command, owner) {
    requireValid('ShellAuthStreamCommand', command);
    const stream = this.get(command.streamId, owner);
    if (
      command.shellEpoch !== stream.context.epoch ||
      command.expectedRevision !== stream.context.revision
    )
      throw fault('common.conflict');
    stream.touched = performance.now();
    if (command.action === 'cancel' || command.action === 'close')
      return this.end(stream, 'closed');
    if (command.action === 'credit') {
      if (stream.credit + command.creditBytes > 1048576) {
        this.end(stream, 'error', 'common.rate_limited');
        throw fault('common.rate_limited');
      }
      if (stream.reply) throw fault('common.conflict');
      const response = new Promise((resolve) => {
        stream.reply = resolve;
      });
      stream.credit += command.creditBytes;
      this.flush(stream);
      return response;
    }
    const upload = this.uploads.get(stream.uploadId);
    const bytes = Buffer.from(command.data, 'base64');
    if (
      !upload ||
      bytes.length > 65536 ||
      bytes.toString('base64') !== command.data ||
      command.sequence !== upload.next ||
      upload.received + bytes.length > upload.sizeBytes
    ) {
      this.end(stream, 'error', 'common.validation_failed');
      throw fault('common.validation_failed');
    }
    const fd = openSync(safePath(upload.file), 'a');
    try {
      writeSync(fd, bytes);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    upload.received += bytes.length;
    upload.next++;
    const event = this.event(stream, 'upload_ack', {
      ackSequence: command.sequence,
      creditBytes: bytes.length,
    });
    this.deliver(stream, event);
    return event;
  }
  openUpload(input, context, owner) {
    if (!this.directory) throw fault('common.unavailable');
    if (
      [...this.uploads.values()].reduce((total, u) => total + u.sizeBytes, 0) +
        input.sizeBytes >
      67108864
    )
      throw fault('common.rate_limited');
    const stream = this.create(context, owner, () => {}, true),
      uploadId = randomUUID();
    const file = safePath(join(this.directory, uploadId + '.upload'));
    const fd = openSync(file, 'wx', 0o600);
    closeSync(fd);
    stream.uploadId = uploadId;
    this.uploads.set(uploadId, {
      ...input,
      context,
      owner,
      stream,
      file,
      received: 0,
      next: 0,
      finished: false,
      until: performance.now() + 300000,
    });
    return {
      statusCode: 200,
      body: {
        uploadId,
        streamId: stream.id,
        maxChunkBytes: 65536,
        creditBytes: 1048576,
      },
    };
  }
  finishUpload(id, context, owner) {
    const upload = this.uploads.get(id);
    if (
      !upload ||
      upload.owner !== owner ||
      upload.finished ||
      upload.until <= performance.now() ||
      upload.context.epoch !== context.epoch ||
      upload.context.revision !== context.revision
    )
      throw fault('common.forbidden');
    const bytes = readFileSync(safePath(upload.file));
    if (
      bytes.length !== upload.sizeBytes ||
      createHash('sha256').update(bytes).digest('hex') !== upload.sha256
    ) {
      this.end(upload.stream, 'error', 'resource.digest_mismatch');
      throw fault('resource.digest_mismatch');
    }
    upload.finished = true;
    this.end(upload.stream, 'end');
    return {
      statusCode: 200,
      body: {
        uploadId: id,
        sizeBytes: upload.sizeBytes,
        sha256: upload.sha256,
      },
    };
  }
  consume(reference, context, owner) {
    const upload = this.uploads.get(reference.uploadId);
    if (
      !upload ||
      !upload.finished ||
      upload.owner !== owner ||
      upload.until <= performance.now() ||
      upload.context.epoch !== context.epoch ||
      upload.context.revision !== context.revision ||
      reference.sha256 !== upload.sha256 ||
      reference.sizeBytes !== upload.sizeBytes
    )
      throw fault('common.forbidden');
    const bytes = readFileSync(safePath(upload.file));
    if (
      bytes.length !== upload.sizeBytes ||
      createHash('sha256').update(bytes).digest('hex') !== upload.sha256
    )
      throw fault('resource.digest_mismatch');
    this.uploads.delete(reference.uploadId);
    this.removeUpload(upload);
    return { bytes, mediaType: upload.mediaType };
  }
  removeUpload(upload) {
    if (upload.file && existsSync(upload.file))
      unlinkSync(safePath(upload.file));
  }
  close() {
    clearInterval(this.timer);
    this.changed();
    this.broker.off('changed', this.changed);
    this.streams.clear();
  }
}
