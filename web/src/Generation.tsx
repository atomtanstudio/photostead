import { thumbURL, workflowURL, type Generation } from './api';
import { Copy as CopyButton } from './copy';
import { Icon } from './icons';
import { placeholder } from './thumbhash';

/** Where the evidence came from, in words. */
function evidence(g: Generation): string {
  const video = g.evidence.startsWith('video:');
  const file = video ? 'video' : 'file';
  switch (g.source) {
    case 'comfyui':
      return `From the ComfyUI workflow saved in the ${file}.`;
    case 'a1111':
      return `From the generation settings saved in the ${file}.`;
    case 'midjourney':
      return `From Midjourney’s prompt and job ID in the ${file}.`;
    case 'grok':
      return `From Grok Imagine’s signature in the ${file}.`;
    case 'aigc':
      return `From the AI-generated content label in the ${file} (China’s GB 45438).`;
    case 'c2pa':
      return `From the ${file}’s Content Credentials (C2PA)${g.tool ? `, made by ${g.tool}` : ''}.`;
    case 'iptc':
      return g.evidence === 'xmp:credit'
        ? `From the ${file}’s label: ${g.kind === 'edited' ? 'Edited' : 'Made'} with ${g.tool ?? 'AI'}.`
        : `From the ${file}’s IPTC label: made with a generative model.`;
  }
  return '';
}

const fmt = (v: unknown) => (typeof v === 'number' ? String(Math.round(v * 1000) / 1000) : String(v));

/** The settings as one block in the Automatic1111 style, which most tools can read back. */
function asText(g: Generation): string {
  const parts: string[] = [];
  if (g.steps) parts.push(`Steps: ${g.steps}`);
  if (g.sampler) parts.push(`Sampler: ${g.sampler}`);
  if (g.scheduler) parts.push(`Schedule type: ${g.scheduler}`);
  if (g.cfg) parts.push(`CFG scale: ${fmt(g.cfg)}`);
  if (g.seed) parts.push(`Seed: ${g.seed}`);
  if (g.width && g.height) parts.push(`Size: ${g.width}x${g.height}`);
  if (g.model) parts.push(`Model: ${g.model}`);
  if (g.loras.length) parts.push(`LoRAs: ${g.loras.map((l) => (l.weight != null ? `${l.name}:${fmt(l.weight)}` : l.name)).join(', ')}`);
  const lines = [g.prompt ?? ''];
  if (g.negative) lines.push(`Negative prompt: ${g.negative}`);
  if (parts.length) lines.push(parts.join(', '));
  return lines.filter(Boolean).join('\n');
}

export function Copy({ text, label }: { text: string; label: string }) {
  return <CopyButton text={text} label={label} className="g-btn" icon={(copied) => (copied ? <Icon.check /> : <Icon.copy />)} />;
}

/** How an item was made, from the evidence in its own metadata. */
export function GenerationPanel({ g, id, fileName, onLeave, onFrom }: {
  g: Generation;
  id: string;
  fileName: string;
  onLeave: () => void;
  onFrom?: (id: string) => void; // show the image a clip was made from
}) {
  const kv: [string, string][] = [];
  if (g.seed) kv.push(['Seed', g.seed]);
  if (g.steps) kv.push(['Steps', String(g.steps)]);
  if (g.cfg) kv.push(['CFG', fmt(g.cfg)]);
  if (g.sampler) kv.push(['Sampler', g.sampler]);
  if (g.scheduler) kv.push(['Scheduler', g.scheduler]);
  if (g.width && g.height) kv.push(['Size', `${g.width} × ${g.height}`]);
  for (const [k, v] of Object.entries(g.settings ?? {}).slice(0, 10)) kv.push([k.replace(/_/g, ' '), fmt(v)]);
  const base = fileName.replace(/\.[^.]+$/, '') || 'workflow';
  const fromPh = g.from && placeholder(g.from.id, g.from.thumbhash);
  return (
    <section className="gen" aria-label="How it was made">
      <h4>
        <Icon.spark />
        {g.kind === 'edited' ? 'AI edit' : 'Generation'}
        {g.tool && <span className="g-tool">{g.tool}</span>}
      </h4>
      {g.from && (
        <button type="button" className="g-from" onClick={() => onFrom?.(g.from!.id)} title="Show the image this clip was made from">
          <img src={thumbURL(g.from.id)} alt="" decoding="async" style={{ backgroundImage: fromPh ? `url(${fromPh})` : undefined }} />
          <span>
            <small>Made from</small>
            The start image
          </span>
          <Icon.back />
        </button>
      )}
      {g.prompt && (
        <div className="g-prompt">
          <p>{g.prompt}</p>
          <div className="g-row">
            <Copy text={g.prompt} label="Copy prompt" />
            {(g.promptUses ?? 0) > 1 && g.promptId && (
              <a className="g-uses" href={`#/prompts/${g.promptId}`} onClick={onLeave} title="Show everything made with this prompt">
                {g.promptUses!.toLocaleString()} items use this prompt
              </a>
            )}
          </div>
        </div>
      )}
      {g.negative && (
        <details className="g-neg">
          <summary>Negative prompt</summary>
          <p>{g.negative}</p>
        </details>
      )}
      {(g.model || g.loras.length > 0) && (
        <div className="g-chips">
          {g.model &&
            (g.modelId ? (
              <a className="g-chip" href={`#/models/${g.modelId}`} onClick={onLeave} title="Model: show everything made with it">
                <Icon.layers />
                {g.model}
              </a>
            ) : (
              <span className="g-chip" title="Model">
                <Icon.layers />
                {g.model}
              </span>
            ))}
          {g.loras.map((l) => (
            <a key={l.id} className="g-chip lora" href={`#/loras/${l.id}`} onClick={onLeave} title="LoRA: show everything made with it">
              {l.name}
              {l.weight != null && <i>{fmt(l.weight)}</i>}
            </a>
          ))}
        </div>
      )}
      {kv.length > 0 && (
        <dl className="g-kv">
          {kv.map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
      )}
      {(g.prompt || kv.length > 0 || g.hasWorkflow) && (
        <div className="g-row">
          {(g.prompt || kv.length > 0) && <Copy text={asText(g)} label="Copy all" />}
          {g.hasWorkflow && (
            <a className="g-btn" href={workflowURL(id)} download={`${base}-workflow.json`} title="Drop it on ComfyUI to load it">
              <Icon.download />
              Workflow
            </a>
          )}
        </div>
      )}
      <p className="g-note">{evidence(g)}</p>
      {g.incomplete && (
        <p className="g-note">
          {g.prompt ? 'The full workflow is in the file and will be read in shortly.' : 'The prompt and settings are in the file and will be read in shortly.'}
        </p>
      )}
    </section>
  );
}
