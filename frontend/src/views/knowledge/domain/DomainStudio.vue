<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { get, getDown, post, put } from '@/utils/request'
import { listModels, type ModelConfig } from '@/api/model'
import { dashboardQuery, failedPageJobs, moveCursor } from './dashboard'

type Stage = {
  id: string
  model_ref: string
  prompt: string
  schema: Record<string, unknown>
  inputs: Record<string, { source: string; stage_id?: string }>
  optional?: boolean
  enabled?: boolean
  input_tokens?: number
  output_tokens?: number
  timeout_seconds?: number
}
type Profile = { id: string; revision: string; name: string; stages: Stage[] }
type Revision = Pick<Profile, 'id' | 'revision'>
type Binding = { profile_id: string; revision: string; enabled: boolean; version: number }
type Job = { file_name: string; title: string; current: boolean; id: string; document_id: string; profile_id: string; revision: string; status: string; error_code: string; stage_index: number }
type Run = { attempt: number; stage_id: string; raw_available: boolean; valid_available: boolean; prompt_tokens: number; completion_tokens: number; error_code: string; reused: boolean; input_coverage?: { mode: string; completed_parts: number; parts: number } }

const props = defineProps<{ kbId: string }>()
const emit = defineEmits<{ openSource: [id: string] }>()
const available = ref(false)
const visible = ref(false)
const error = ref('')
const notice = ref('')
const pending = ref(false)
const editor = ref('')
const example = ref('')
const schema = ref('')
const stageIndex = ref(0)
const revisions = ref<Revision[]>([])
const models = ref<ModelConfig[]>([])
const binding = ref<Binding | null>(null)
const selection = ref('')
const enabled = ref(true)
const jobs = ref<Job[]>([])
const status = ref('')
const search = ref('')
const cursors = ref<string[]>([''])
const next = ref('')
const counts = ref<Record<string, number>>({})
const usage = ref<Record<string, number>>({})
const packageFile = ref<File | null>(null)
const packagePreview = ref('')
type Dashboard = { jobs: Job[]; next?: string; counts: Record<string, number>; usage: Record<string, number> }

const current = ref<Job | null>(null)
const runs = ref<Run[]>([])
const artifact = ref('')
const coverage = ref<Record<string, unknown> | null>(null)
const endpoint = computed(() => '/api/v1/knowledge-bases/' + encodeURIComponent(props.kbId) + '/domain')
const root = '/api/v1/domain-profiles'

const revisionKey = (revision: Revision) => JSON.stringify([revision.id, revision.revision])
const profile = computed<Profile | null>(() => {
  try {
    const value = JSON.parse(editor.value)
    return value && Array.isArray(value.stages) ? value : null
  } catch { return null }
})
const stage = computed(() => profile.value?.stages[stageIndex.value])
watch(stage, value => { schema.value = value ? JSON.stringify(value.schema, null, 2) : '' })

function message(reason: unknown) {
  error.value = (reason as { message?: string })?.message || 'The domain operation failed.'
}

function write(profile: Profile) { editor.value = JSON.stringify(profile, null, 2) }
function editStage(update: Partial<Stage>) {
  if (!profile.value || !stage.value) return
  profile.value.stages[stageIndex.value] = { ...stage.value, ...update }
  write(profile.value)
}

function newProfile() {
  const base = { $schema: 'https://json-schema.org/draft/2020-12/schema', type: 'object' }
  write({ id: '', revision: '1.0.0', name: '', stages: [
    { id: 'extract', model_ref: '', prompt: '', schema: base, inputs: { document: { source: 'document' } } },
    { id: 'analyze', model_ref: '', prompt: '', schema: base, inputs: { extraction: { source: 'stage_output', stage_id: 'extract' } } },
  ] })
  stageIndex.value = 0
}

async function reloadBinding() {
  const path = endpoint.value
  const result = await get<{ data: Binding | null }>(path + '/binding')
  if (path !== endpoint.value) return
  binding.value = result.data
  selection.value = binding.value ? revisionKey({ id: binding.value.profile_id, revision: binding.value.revision }) : ''
  enabled.value = binding.value?.enabled ?? true
}

watch(() => props.kbId, async (id) => {
  available.value = false
  visible.value = false
  current.value = null
  if (!id) return
  try { await reloadBinding(); if (props.kbId === id) available.value = true } catch { /* hidden when unavailable */ }
}, { immediate: true })

async function refresh() {
  const path = endpoint.value
  const id = current.value?.id
  const query = dashboardQuery(status.value, search.value, cursors.value)
  const result = await get<{ data: Dashboard }>(path + '/jobs?' + query)
  if (path !== endpoint.value || query !== dashboardQuery(status.value, search.value, cursors.value)) return
  jobs.value = result.data.jobs
  next.value = result.data.next || ''
  counts.value = result.data.counts
  usage.value = result.data.usage
  if (id && current.value?.id === id) {
    current.value = jobs.value.find(job => job.id === id) || null
    if (!current.value) { runs.value = []; artifact.value = ''; coverage.value = null; return }
    const result = await get<{ data: Run[] }>(path + '/jobs/' + id + '/runs')
    if (path === endpoint.value && current.value?.id === id) runs.value = result.data
  }
}

function changeFilter() { cursors.value = ['']; void refresh().catch(message) }
function changePage(forward: boolean) {
  cursors.value = moveCursor(cursors.value, forward, next.value)
  void refresh().catch(message)
}
const retryable = computed(() => failedPageJobs(jobs.value))
async function batchRetry() {
  try {
    const result = await post<{ data: { retried: number; failed: string[] } }>(endpoint.value + '/jobs/batch-retry', { ids: retryable.value.map(job => job.id) })
    notice.value = `Queued ${result.data.retried} retries. ${result.data.failed.length} could not be retried.`
    await refresh()
  } catch (reason) { message(reason) }
}
async function submitPackage(action: 'preview' | 'publish') {
  if (!packageFile.value) return
  try {
    const result = await post<{ data: unknown }>(root + '/package/' + action, packageFile.value, { headers: { 'Content-Type': 'application/octet-stream' } })
    packagePreview.value = JSON.stringify(result.data, null, 2)
    if (action === 'publish') {
      revisions.value = (await get<{ data: Revision[] }>(root)).data
      notice.value = 'Package published.'
    }
  } catch (reason) { message(reason) }
}
async function exportPackage() {
  if (!selection.value) return
  const [id, revision] = JSON.parse(selection.value)
  try {
    const file = await getDown(root + '/' + encodeURIComponent(id) + '/' + encodeURIComponent(revision) + '/package')
    download(file, 'domain-profile.zip')
  } catch (reason) { message(reason) }
}

let timer: ReturnType<typeof setInterval> | undefined
watch(visible, async open => {
  if (timer) clearInterval(timer)
  if (!open) return
  error.value = ''
  try {
    const [profiles, modelList] = await Promise.all([
      get<{ data: Revision[] }>(root).catch(() => ({ data: [] })),
      listModels('KnowledgeQA').catch(() => []),
      refresh(),
    ])
    revisions.value = profiles.data
    models.value = modelList
  } catch (reason) { message(reason) }
  if (visible.value) timer = setInterval(() => { void refresh().catch(message) }, 6000)
})
onBeforeUnmount(() => { if (timer) clearInterval(timer) })

async function submit(action: string) {
  pending.value = true
  error.value = ''
  notice.value = ''
  try {
    const response = await post<{ data: { sha256: string } }>(root + '/' + action, editor.value, {
      headers: { 'Content-Type': 'application/json' },
    })
    notice.value = (action === 'publish' ? 'Published revision ' : 'Manifest validated ') + response.data.sha256.slice(0, 12)
    if (action === 'publish') revisions.value = (await get<{ data: Revision[] }>(root)).data
  } catch (reason) { message(reason) }
  finally { pending.value = false }
}

async function loadRevision() {
  if (!selection.value) return
  const [id, revision] = JSON.parse(selection.value)
  try {
    const blob = await getDown(root + '/' + encodeURIComponent(id) + '/' + encodeURIComponent(revision))
    editor.value = await blob.text()
    stageIndex.value = 0
  } catch (reason) { message(reason) }
}

function copyProfile() {
  if (!profile.value) return
  const draft = { ...profile.value, id: '', revision: '1.0.0' }
  write(draft)
}

function download(blob: Blob, name: string) {
  const link = document.createElement('a')
  link.href = URL.createObjectURL(blob)
  link.download = name
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(link.href), 1000)
}
function exportManifest() {
  download(new Blob([editor.value], { type: 'application/json' }), 'domain-profile.json')
}

async function saveBinding() {
  if (!selection.value) return
  const [profile_id, revision] = JSON.parse(selection.value)
  try {
    await put(endpoint.value + '/binding', { profile_id, revision, enabled: enabled.value, version: binding.value?.version || 0 })
    await reloadBinding()
    notice.value = 'Knowledge base configuration saved.'
  } catch (reason) { message(reason) }
}

async function importFile(event: Event, mode: string) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    if (file.size > 1024 * 1024) throw new Error('Choose a file smaller than 1 MiB.')
    if (mode === 'manifest') { editor.value = await file.text(); stageIndex.value = 0; return }
    if (mode === 'schema') { editStage({ schema: JSON.parse(await file.text()) }); return }
    const format = mode === 'example' ? 'example' : file.name.toLowerCase().endsWith('.docx') ? 'docx' : 'txt'
    const response = await post<{ data: { text: string; warning?: string } }>(root + '/preview/' + format, file, {
      headers: { 'Content-Type': 'application/octet-stream' },
    })
    if (format === 'example') { example.value = response.data.text; notice.value = response.data.warning || '' }
    else { editStage({ prompt: response.data.text }); notice.value = 'Prompt imported. Review the extracted text before publishing.' }
  } catch (reason) { message(reason) }
}

function saveSchema() {
  try { editStage({ schema: JSON.parse(schema.value) }) } catch (reason) { message(reason) }
}

async function openJob(job: Job) {
  current.value = job
  runs.value = []
  artifact.value = ''
  coverage.value = null
  try { await refresh() } catch (reason) { message(reason) }
}
const jobPath = computed(() => endpoint.value + '/jobs/' + current.value?.id)
async function jobAction(action: string) {
  try { await post(jobPath.value + '/' + action); await refresh() } catch (reason) { message(reason) }
}
async function parsePreview(save = false) {
  try {
    const response = await get<{ data: { normalized_markdown: string; coverage: Record<string, unknown> } }>(jobPath.value + '/parse')
    if (save) download(new Blob([JSON.stringify(response.data, null, 2)], { type: 'application/json' }), 'parse-snapshot.json')
    else {
      artifact.value = response.data.normalized_markdown
      coverage.value = response.data.coverage
    }
  } catch (reason) { message(reason) }
}
async function stageArtifact(run: Run, kind: string, save = false) {
  try {
    const blob = await getDown(jobPath.value + '/runs/' + run.attempt + '/' + kind)
    if (save) download(blob, run.stage_id + (kind === 'valid' ? '.json' : '.txt'))
    else artifact.value = await blob.text()
  } catch (reason) { message(reason) }
}
async function original() {
  if (!current.value) return
  try {
    const blob = await getDown('/api/v1/knowledge/' + encodeURIComponent(current.value.document_id) + '/download')
    download(blob, current.value.document_id)
  } catch (reason) { message(reason) }
}
</script>

<template>
  <t-button v-if="available" variant="outline" size="small" @click="visible = true">Domain engine</t-button>
  <t-dialog v-model:visible="visible" header="Domain engine" width="min(980px, 96vw)" :footer="false">
    <p v-if="error" class="domain-error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <t-tabs default-value="jobs">
      <t-tab-panel value="jobs" label="Processing">
        <p>Upload a file through the knowledge base to start processing automatically when a profile is enabled.</p>
        <div class="domain-actions">
          <label>Search file or title <input v-model="search" maxlength="300" @change="changeFilter"></label>
          <label>Status <select v-model="status" @change="changeFilter"><option value="">All</option>
            <option v-for="(_, key) in counts" :key="key" :value="key">{{ key }}</option></select></label>
          <t-button variant="outline" size="small" @click="refresh().catch(message)">Refresh</t-button>
          <t-button variant="outline" size="small" :disabled="!retryable.length" @click="batchRetry">Retry failures on this page</t-button>
        </div>
        <p aria-label="Knowledge base job totals"><span v-for="(count, key) in counts" :key="key">{{ key }}: {{ count }} · </span></p>
        <p v-if="Object.keys(usage).length">Reported tokens {{ usage.prompt_tokens }} input / {{ usage.completion_tokens }} output · {{ usage.model_calls }} tracked calls · {{ usage.unknown_calls }} without usage · {{ usage.reused_runs }} reused · {{ usage.legacy_runs }} legacy runs</p>
        <div v-if="!jobs.length" class="domain-empty">No processing jobs for this knowledge base.</div>
        <div v-for="job in jobs" :key="job.id" class="domain-job">
          <t-button variant="text" @click="openJob(job)">{{ job.title || job.file_name || job.document_id }}</t-button>
          <span v-if="job.file_name && job.file_name !== job.title">{{ job.file_name }}</span>
          <span>{{ job.status }} · {{ job.profile_id }} {{ job.revision }} · {{ job.current ? 'current binding' : 'history' }}</span>
          <span v-if="job.error_code" class="domain-error">{{ job.error_code }}</span>
        </div>
        <div class="domain-actions">
          <t-button size="small" :disabled="cursors.length < 2" @click="changePage(false)">Previous</t-button>
          <t-button size="small" :disabled="!next" @click="changePage(true)">Next</t-button>
        </div>
        <section v-if="current" class="domain-detail">
          <div class="domain-actions">
            <t-button size="small" variant="outline" @click="visible = false; emit('openSource', current.document_id)">View original</t-button>
            <t-button size="small" variant="outline" @click="original">Download original</t-button>
            <t-button size="small" variant="outline" @click="parsePreview()">Parsed text &amp; coverage</t-button>
            <t-button size="small" variant="outline" @click="parsePreview(true)">Save parse report</t-button>
            <t-button v-if="['succeeded', 'failed', 'needs_review'].includes(current.status)" size="small" variant="outline" @click="jobAction('reprocess')">Run current binding</t-button>
            <t-button v-if="['failed', 'capture_failed'].includes(current.status)" size="small" @click="jobAction('retry')">Retry</t-button>
            <t-button v-if="['waiting_parse', 'queued', 'running', 'retry_wait', 'capture_failed', 'needs_review'].includes(current.status)" size="small" variant="outline" @click="jobAction('cancel')">Cancel</t-button>
          </div>
          <div v-for="run in runs" :key="run.attempt" class="domain-job">
            <strong>{{ run.stage_id }} · {{ run.attempt }}</strong>
            <span>{{ run.prompt_tokens || '—' }} input / {{ run.completion_tokens || '—' }} output tokens</span>
            <span v-if="run.reused">Reused</span>
            <span v-if="run.input_coverage?.mode">{{ run.input_coverage.mode }} · {{ run.input_coverage.completed_parts }}/{{ run.input_coverage.parts }}</span>
            <span v-if="run.error_code" class="domain-error">{{ run.error_code }}</span>
            <t-button v-if="run.raw_available" size="small" variant="text" @click="stageArtifact(run, 'raw')">Raw</t-button>
            <t-button v-if="run.raw_available" size="small" variant="text" @click="stageArtifact(run, 'raw', true)">Save raw</t-button>
            <t-button v-if="run.valid_available" size="small" variant="text" @click="stageArtifact(run, 'valid')">JSON</t-button>
            <t-button v-if="run.valid_available" size="small" variant="text" @click="stageArtifact(run, 'valid', true)">Save JSON</t-button>
          </div>
          <dl v-if="coverage" class="domain-coverage">
            <template v-for="(value, key) in coverage" :key="key"><dt>{{ key }}</dt><dd>{{ value }}</dd></template>
          </dl>
          <pre v-if="artifact" tabindex="0">{{ artifact }}</pre>
        </section>
      </t-tab-panel>
      <t-tab-panel value="profiles" label="Profiles &amp; configuration">
        <div class="domain-actions">
          <label>Published revision
            <select v-model="selection"><option value="">Select a revision</option>
              <option v-for="item in revisions" :key="revisionKey(item)" :value="revisionKey(item)">{{ item.id }} · {{ item.revision }}</option>
            </select>
          </label>
          <t-button size="small" variant="outline" :disabled="!selection" @click="loadRevision">Load</t-button>
          <label><input v-model="enabled" type="checkbox"> Automatic processing</label>
          <t-button size="small" :disabled="!selection" @click="saveBinding">Save binding</t-button>
        </div>
        <div class="domain-actions">
          <t-button size="small" variant="outline" @click="newProfile">New profile</t-button>
          <t-button size="small" variant="outline" :disabled="!profile" @click="copyProfile">Copy draft</t-button>
          <label>Import JSON manifest <input type="file" accept=".json" @change="importFile($event, 'manifest')"></label>
          <t-button size="small" variant="outline" :disabled="!editor" @click="exportManifest">Export draft</t-button>
        </div>
        <div class="domain-actions">
          <label>Complete profile ZIP <input type="file" accept=".zip" @change="packageFile = ($event.target as HTMLInputElement).files?.[0] || null; packagePreview = ''"></label>
          <t-button size="small" :disabled="!packageFile" @click="submitPackage('preview')">Preview package</t-button>
          <t-button size="small" :disabled="!packagePreview" @click="submitPackage('publish')">Publish package</t-button>
          <t-button size="small" :disabled="!selection" @click="exportPackage">Download published package</t-button>
        </div>
        <pre v-if="packagePreview">{{ packagePreview }}</pre>
        <details v-if="stage" class="domain-detail">
          <summary>Stage editor</summary>
          <div class="domain-actions">
            <label>Stage <select v-model="stageIndex"><option v-for="(item, index) in profile?.stages" :key="index" :value="index">{{ item.id }}</option></select></label>
            <label>Model
              <select :value="stage.model_ref" @change="editStage({ model_ref: ($event.target as HTMLSelectElement).value })">
                <option :value="stage.model_ref">{{ stage.model_ref || 'Select chat model' }}</option>
                <option v-for="model in models" :key="model.id" :value="model.id">{{ model.name }}</option>
              </select>
            </label>
            <label v-if="stage.optional"><input type="checkbox" :checked="stage.enabled" @change="editStage({ enabled: ($event.target as HTMLInputElement).checked })"> Run optional stage</label>
          </div>
          <div class="domain-actions">
            <label>Input tokens <input type="number" min="0" :value="stage.input_tokens || 0" @change="editStage({ input_tokens: Number(($event.target as HTMLInputElement).value) })"></label>
            <label>Output tokens <input type="number" min="0" :value="stage.output_tokens || 0" @change="editStage({ output_tokens: Number(($event.target as HTMLInputElement).value) })"></label>
            <label>Timeout seconds <input type="number" min="0" max="1800" :value="stage.timeout_seconds || 0" @change="editStage({ timeout_seconds: Number(($event.target as HTMLInputElement).value) })"></label>
            <span>0 uses defaults</span>
          </div>
          <label>Prompt <textarea :value="stage.prompt" rows="4" @change="editStage({ prompt: ($event.target as HTMLTextAreaElement).value })" /></label>
          <label>Import TXT or DOCX prompt <input type="file" accept=".txt,.docx" @change="importFile($event, 'prompt')"></label>
          <label>Formal JSON Schema <textarea v-model="schema" rows="5" /></label>
          <div class="domain-actions">
            <t-button size="small" variant="outline" @click="saveSchema">Apply schema to draft</t-button>
            <label>Import schema <input type="file" accept=".json" @change="importFile($event, 'schema')"></label>
            <label>Preview example <input type="file" accept=".json" @change="importFile($event, 'example')"></label>
          </div>
          <pre v-if="example">{{ example }}</pre>
        </details>
        <label>Manifest <textarea v-model="editor" rows="11" spellcheck="false" /></label>
        <div class="domain-actions">
          <t-button :loading="pending" :disabled="!editor" variant="outline" @click="submit('validate')">Validate</t-button>
          <t-button :loading="pending" :disabled="!editor" @click="submit('publish')">Publish revision</t-button>
        </div>
      </t-tab-panel>
    </t-tabs>
  </t-dialog>
</template>

<style scoped>
.domain-actions, .domain-job { display: flex; flex-wrap: wrap; gap: 8px 14px; align-items: center; margin-block: 12px; }
.domain-job, .domain-detail { border: 1px solid var(--td-component-border); border-radius: 6px; padding: 10px; }
.domain-error { color: var(--td-error-color); }
.domain-empty { padding: 20px 0; color: var(--td-text-color-secondary); }
.domain-coverage { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; }
.domain-coverage dd { margin: 0; }
label textarea { display: block; width: 100%; box-sizing: border-box; margin: 6px 0 10px; }
textarea, select { border: 1px solid var(--td-component-border); border-radius: 4px; padding: 6px; color: var(--td-text-color-primary); background: var(--td-bg-color-container); }
pre { max-height: 340px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; background: var(--td-bg-color-component); border-radius: 6px; padding: 12px; }
</style>
