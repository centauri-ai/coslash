import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { TooltipProvider } from '@/components/ui/tooltip';
import { CoslashPage } from '@/pages/coslash/CoslashPage';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <TooltipProvider>
      <CoslashPage />
    </TooltipProvider>
  </StrictMode>,
);
