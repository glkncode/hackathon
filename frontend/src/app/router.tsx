import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from '../features/auth';
import { ChatPage } from '../pages/ChatPage/ChatPage';
import { DocumentsPage } from '../pages/DocumentsPage/DocumentsPage';
import { LoginPage } from '../pages/LoginPage/LoginPage';
import { NotFoundPage } from '../pages/NotFoundPage/NotFoundPage';
import { TemplateDraftPage } from '../pages/TemplateDraftPage/TemplateDraftPage';
import { TemplateRegisterPage } from '../pages/TemplateRegisterPage/TemplateRegisterPage';
import { TemplateResultPage } from '../pages/TemplateResultPage/TemplateResultPage';
import { TemplatesPage } from '../pages/TemplatesPage/TemplatesPage';
import { AppLayout } from './layout/AppLayout';

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
