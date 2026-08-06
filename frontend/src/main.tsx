import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { CoslashPage } from '@/pages/coslash/CoslashPage';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <CoslashPage />
  </StrictMode>,
);
