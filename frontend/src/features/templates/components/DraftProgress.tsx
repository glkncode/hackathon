import { progress } from '../lib/templateDisplay';
import type { TemplateDraft } from '../types/templates';
import styles from './DraftProgress.module.css';

export function DraftProgress({ draft }: { draft: TemplateDraft }) {
  const p = progress(draft);
  return (
    <div className={styles['template-progress']}>
      <div>
        <strong>
          {p.completed} из {p.total} обязательных полей
        </strong>
        <span>{p.percent}%</span>
      </div>
      <progress value={p.percent} max={100} aria-label="Готовность черновика" />
    </div>
  );
}
