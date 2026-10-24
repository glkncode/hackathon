import { ArrowUpRight, BookOpen, FileCheck2, FileText, Search } from 'lucide-react';
import ui from '../../../shared/ui/ui.module.css';
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
      <div className={styles['welcome-mark']}>
        <FileText size={31} />
        <span>
          <Search size={15} />
        </span>
      </div>
      <span className={ui['eyebrow']}>ВАШ ПОМОЩНИК ПО ДОКУМЕНТАМ</span>
      <h1>
        Здравствуйте! Я помогу разобраться
        <br /> во внутренних инструкциях
        <br /> и регламентах
      </h1>
      <p>Задайте вопрос, и я найду подходящие положения документов.</p>
      <div className={styles['example-grid']}>
        {examples.map(({ icon: Icon, title, question }) => (
          <button
            key={title}
            className={styles['example-card']}
            disabled={!ready || pending}
            onClick={() => onSelect(question)}
          >
            <span className={styles['example-icon']}>
              <Icon size={19} />
            </span>
            <strong>{title}</strong>
            <span className={styles['example-question']}>{question}</span>
            <ArrowUpRight className={styles['example-arrow']} size={16} />
          </button>
        ))}
      </div>
      <div className={styles['welcome-caption']}>
        <Search size={14} />
        Примеры вопросов. Результаты появятся после поиска.
      </div>
    </div>
  );
}
