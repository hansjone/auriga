const text = (message) =>
  typeof message?.content === 'string'
    ? message.content
    : (message?.content ?? [])
        .filter((part) => part.type === 'text')
        .map((part) => part.text)
        .join('\n');

function result(message) {
  let value = text(message);
  for (let depth = 0; depth < 6; depth++) {
    if (typeof value === 'string') {
      try {
        value = JSON.parse(value);
      } catch {
        return null;
      }
    } else if (value?.status) return value;
    else if (value?.result) value = value.result;
    else if (value?.content) value = text(value);
    else return null;
  }
  return null;
}

export function nativeAssistantEvidence(events, marker) {
  for (const event of events) {
    if (event.type !== 'assistant/message' || event.data?.interrupted) continue;
    const usage = event.data.usage;
    if (
      text(event.data.message).trim() === marker &&
      Number.isSafeInteger(usage?.inputTokens) &&
      usage.inputTokens >= 0 &&
      Number.isSafeInteger(usage?.outputTokens) &&
      usage.outputTokens >= 0 &&
      usage.inputTokens + usage.outputTokens > 0
    )
      return {
        markerObserved: true,
        inputTokens: usage.inputTokens,
        outputTokens: usage.outputTokens,
      };
  }
  return null;
}

// Pair the real tool events; model prose cannot supply a successful tool result.
export function nativeToolEvidence(events, name, accepts) {
  for (const event of events) {
    if (event.type !== 'tool/call' || event.data.name !== name) continue;
    let args;
    try {
      args = JSON.parse(event.data.arguments);
    } catch {
      continue;
    }
    if (!accepts(args)) continue;
    const end = events.find(
      (item) =>
        item.type === 'tool/result' &&
        item.data.message?.toolCallId === event.data.callId,
    );
    if (!end || end.data.message.isError === true) continue;
    const value = result(end.data.message);
    if (value) return { callId: event.data.callId, value };
  }
  return null;
}

// The native host deduplicates durable input by recipient ID. An optional
// trusted operator read of committed inbox state binds that ID to the message;
// model text and an initiating prompt cannot provide this witness.
export function nativeNetworkReceipt(events, marker, witness) {
  for (const event of events) {
    if (event.type !== 'user/message') continue;
    const content = text(event.data);
    const match = content.match(
      /^\[Auriga external message ([A-Za-z0-9-]{1,128})\]\r?\n/,
    );
    const witnessed =
      match &&
      witness?.stage === 'committed' &&
      typeof witness.sessionId === 'string' &&
      witness.sessionId.length > 0 &&
      typeof witness.recipientId === 'string' &&
      /^[A-Za-z0-9-]{1,128}$/.test(witness.recipientId) &&
      witness.marker === marker &&
      witness.messageId === match[1] &&
      witness.recipientId === event.data.source?.rpcId;
    if (
      match &&
      content.slice(match[0].length) === marker &&
      event.data.source?.kind === 'user' &&
      (event.data.source.rpcId === match[1] || witnessed)
    )
      return {
        marker,
        messageId: match[1],
        ...(witnessed ? { recipientId: witness.recipientId } : {}),
        via: 'native-session-user-message',
      };
  }
  return null;
}
