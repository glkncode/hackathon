import { createBrowserRouter, Navigate } from 'react-router-dom';
import { AppLayout } from '../components/layout/AppLayout';
import { ProtectedRoute } from '../features/auth/ProtectedRoute';
import { ChatPage } from '../pages/ChatPage';
import { DocumentsPage } from '../pages/DocumentsPage';
import { LoginPage } from '../pages/LoginPage';
import { NotFoundPage } from '../pages/NotFoundPage';
import { TemplatesPage } from '../pages/TemplatesPage';
import { TemplateDraftPage } from '../pages/TemplateDraftPage';
import { TemplateResultPage } from '../pages/TemplateResultPage';
import { TemplateRegisterPage } from '../pages/TemplateRegisterPage';

export const router = createBrowserRouter([
  { path: '/login', element: <LoginPage /> },
  {
    element: <ProtectedRoute />,
    children: [
      {
        element: <AppLayout />,
        children: [
          { path: '/', element: <Navigate to="/chat" replace /> },
          { path: '/chat', element: <ChatPage /> },
          { path: '/documents', element: <DocumentsPage /> },
          { path: '/templates', element: <TemplatesPage /> },
          { path: '/templates/drafts/:id', element: <TemplateDraftPage /> },
          { path: '/templates/results/:id', element: <TemplateResultPage /> },
          { path: '/templates/register', element: <TemplateRegisterPage /> },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]);
