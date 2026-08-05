import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { CoslashPage } from '@/pages/coslash/CoslashPage';
import './index.css';
import { getTheme, setTheme } from './lib/theme.ts';

setTheme(getTheme(localStorage.getItem('theme')));

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <CoslashPage />
  </StrictMode>,
);
