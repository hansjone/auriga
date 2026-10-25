<script setup lang="ts">
import { computed } from 'vue';
import { ElButton } from 'element-plus';
import type { Node } from '../../admin/api';
import { retirementBlock } from './state';
import { useNodesI18n } from './messages';
const props = defineProps<{ node: Node; canWrite: boolean; locked: boolean }>();
const emit = defineEmits<{ retire: [] }>();
const { t } = useNodesI18n();
const block = computed(() => retirementBlock(props.node));
const metrics = computed(
  () =>
    [
      ['cpu', props.node.usage?.cpuMillisUsed, props.node.capacity.cpuMillis],
      ['memory', props.node.usage?.memoryMbUsed, props.node.capacity.memoryMb],
      ['disk', props.node.usage?.diskGbUsed, props.node.capacity.diskGb],
      [
        'slots',
        props.node.usage?.instancesTotal,
        props.node.capacity.maxInstances,
      ],
    ] as const,
);
</script>
<template>
  <section>
    <p v-if="node.status === 'offline'" role="status">{{ t('heartbeat') }}</p>
    <p>{{ t('stale') }}</p>
    <dl>
      <template v-for="[key, used, capacity] in metrics" :key="key">
        <dt>{{ t(key) }}</dt>
        <dd>
          {{ used ?? t('unknown') }} / {{ capacity ?? t('unknown') }}
          <span
            v-if="
              used !== undefined && capacity !== undefined && used >= capacity
            "
            >{{ t('full') }}</span
          >
        </dd>
      </template>
      <dt>{{ t('version') }}</dt>
      <dd>{{ node.agentVersion }}</dd>
      <dt>{{ t('lastSeen') }}</dt>
      <dd>{{ node.lastSeenAt || t('unknown') }}</dd>
      <dt>{{ t('certificate') }}</dt>
      <dd>{{ node.certificateExpiresAt || t('unknown') }}</dd>
    </dl>
    <p>{{ t('compatibility') }}</p>
    <p v-if="Math.abs(node.clockSkewMs ?? 0) > 5000" role="status">
      {{ t('clock') }}
    </p>
    <p v-if="block" data-testid="retirement-reason">{{ t(block) }}</p>
    <ElButton
      v-if="canWrite"
      type="danger"
      :disabled="locked || !!block"
      @click="!block && !locked && emit('retire')"
      >{{ t('retire') }}</ElButton
    >
  </section>
</template>
