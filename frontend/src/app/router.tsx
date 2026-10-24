import { createBrowserRouter, Navigate } from 'react-router-dom';
import { AppLayout } from '../components/layout/AppLayout';
import { ProtectedRoute } from '../features/auth/ProtectedRoute';
import { ChatPage } from '../pages/ChatPage';
import { DocumentsPage } from '../pages/DocumentsPage';
import { LoginPage } from '../pages/LoginPage';
import { NotFoundPage } from '../pages/NotFoundPage';

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
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]);
