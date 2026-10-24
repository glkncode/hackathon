import { useParams } from 'react-router-dom';
import { useCurrentUserGuard } from '../../features/auth';
import { TemplateDraftEditor } from '../../features/templates';

export function TemplateDraftPage() {
  const { id = '' } = useParams();
  const isCurrentOwner = useCurrentUserGuard();
  return <TemplateDraftEditor id={id} isCurrentOwner={isCurrentOwner} />;
}
