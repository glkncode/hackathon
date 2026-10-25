import { BookOpen, FileCheck2, FileText } from 'lucide-react';
import styles from './ChatWelcome.module.css';

const examples = [
  {
    icon: FileCheck2,
    title: 'Согласование документов',
    question: 'Как согласовать служебную записку?',
  },
  {
    icon: FileText,
    title: 'Правила оформления',
    question: 'Какие требования предъявляются к оформлению документа?',
  },
  {
    icon: BookOpen,
    title: 'Регистрация и учёт',
    question: 'Где найти порядок регистрации документов?',
  },
];

export function ChatWelcome({
  ready,
  pending,
  onSelect,
}: {
  ready: boolean;
  pending: boolean;
  onSelect: (question: string) => void;
}) {
  return (
    <div className={styles['chat-welcome']}>
      <div className={styles.hero}>
        <svg className={styles.circuit} viewBox="0 0 280 300" fill="none" aria-hidden="true">
          <g stroke="currentColor" strokeWidth="1.5">
            <path d="M280 28H160l-38 38v48H74M280 68h-80l-38 38v80H98M280 240H186l-36-36v-48H38M280 272H156l-42-42H58M232 0v30M260 0v88l-40 40v64" />
            <circle cx="74" cy="114" r="5" />
            <circle cx="98" cy="186" r="5" />
            <circle cx="38" cy="156" r="5" />
            <circle cx="58" cy="230" r="5" />
            <circle cx="220" cy="197" r="5" />
          </g>
        </svg>
        <span className={styles['welcome-mark']} aria-hidden="true">
          <FileText size={28} />
        </span>
        <h1>Помощник по внутренним документам</h1>
        <p>
          Находите нужные положения регламентов, разбирайтесь в требованиях и создавайте документы.
        </p>
      </div>
      <div className={styles['example-grid']}>
        {examples.map(({ icon: Icon, title, question }) => (
          <button
            key={title}
            className={styles['example-card']}
            disabled={!ready || pending}
            onClick={() => onSelect(question)}
          >
            <span className={styles['example-icon']} aria-hidden="true">
              <Icon size={22} />
            </span>
            <strong>{title}</strong>
            <span className={styles['example-question']}>{question}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
