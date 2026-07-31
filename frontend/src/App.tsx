import { BrowserRouter, Navigate, Route, Routes } from 'react-router';
import { CoSlashPage } from '@/pages/coslash/CoSlashPage';

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Navigate to="/coslash" replace />} />
        <Route path="/coslash" element={<CoSlashPage />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
