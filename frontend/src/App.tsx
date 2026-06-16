import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import { AppShell } from './components/layout/AppShell'
import { JsonToolPage } from './pages/JsonToolPage'
import { MongoJsonToolPage } from './pages/MongoJsonToolPage'
import { VisualizeToolPage } from './pages/VisualizeToolPage'

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<AppShell />}>
          <Route index element={<Navigate to="/tools/json" replace />} />
          <Route path="tools/json" element={<JsonToolPage />} />
          <Route path="tools/mongodb-json" element={<MongoJsonToolPage />} />
          <Route path="tools/visualize" element={<VisualizeToolPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
