import '@cate/ui/styles.css';
import { useMemo } from 'react';
import { Route, BrowserRouter as Router, Routes } from 'react-router-dom';
import './app.css';
import { Layout } from './components/Layout';
import { ProtectedRoute } from './components/ProtectedRoute';
import { routes } from './config/routes';
import { AuthProvider } from './lib/AuthProvider';

export default function App() {
  const [navItems, footerItems] = useMemo(() => {
    return [
      routes.filter(route => route.showInNav),
      routes.filter(route => !route.showInNav && !route.system),
    ];
  }, []);

  return (
    <Router>
      <AuthProvider>
        <Layout
          routes={{ menu: footerItems, nav: navItems }}
          defaultOpen={true}
          mainContent={
            <Routes>
              {routes.map((route, index) => {
                const Element = route.element;
                const wrappedElement =
                  route.protected !== false ? (
                    <ProtectedRoute>
                      <Element />
                    </ProtectedRoute>
                  ) : (
                    <Element />
                  );
                return <Route key={index} path={route.path} element={wrappedElement} />;
              })}
            </Routes>
          }
        />
      </AuthProvider>
    </Router>
  );
}
