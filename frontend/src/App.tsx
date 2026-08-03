import { BrowserRouter, Navigate, Route, Routes } from 'react-router';
import { CoslashPage } from '@/pages/coslash/CoslashPage';

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Navigate to="/coslash" replace />} />
        <Route path="/coslash" element={<CoslashPage />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
