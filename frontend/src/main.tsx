import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './index.css';
import { CoslashPage } from '@/pages/coslash/CoslashPage';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <CoslashPage />
  </StrictMode>,
);
