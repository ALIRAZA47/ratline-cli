import { createBrowserRouter, RouterProvider } from "react-router-dom";
const api = import.meta.env.VITE_API_URL;
const router = createBrowserRouter([{ path: "/", element: <div>Dashboard {api}</div> }, { path: "/reports/:id", element: <div/> }]);
export default function App() { return <RouterProvider router={router} />; }
