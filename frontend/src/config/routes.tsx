import { P } from '@contenox/ui';
import {
  ChevronsRight,
  Database,
  File,
  Home,
  MessageCircleCode,
  Search,
  Settings,
  Turtle,
  User2Icon,
} from 'lucide-react';
import i18n from '../i18n';
import BackendsPage from '../pages/admin/backends/BackendPage.tsx';
import ChatPage from '../pages/admin/chats/ChatPage.tsx';
import ChatsListPage from '../pages/admin/chats/components/ChatListPage.tsx';
import FilesPage from '../pages/admin/files/FilesPage.tsx';
import ExecPromptPage from '../pages/admin/prompt/ExecPromptPage.tsx';
import SearchPage from '../pages/admin/search/SearchPage.tsx';
import ServerJobsPage from '../pages/admin/serverjobs/ServerJobsPage.tsx';
import UserPage from '../pages/admin/users/UserPage.tsx';
import About from '../pages/public/about/About.tsx';
import ByePage from '../pages/public/bye/Bye.tsx';
import HomePage from '../pages/public/home/Homepage.tsx';
import AuthPage from '../pages/public/login/AuthPage.tsx';
import Privacy from '../pages/public/privacy/Privacy.tsx';
import { LOGIN_ROUTE } from './routeConstants.ts';

interface RouteConfig {
  path: string;
  element: React.ComponentType;
  label: string;
  icon?: React.ReactNode;
  showInNav?: boolean;
  system?: boolean;
  protected: boolean;
}

export const routes: RouteConfig[] = [
  {
    path: '/',
    element: HomePage,
    label: i18n.t('navbar.home'),
    icon: <Home className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: false,
  },
  {
    path: '/backends',
    element: BackendsPage,
    label: i18n.t('navbar.backends'),
    icon: <Database className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/files',
    element: FilesPage,
    label: i18n.t('navbar.files'),
    icon: <File className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/jobs',
    element: ServerJobsPage,
    label: i18n.t('navbar.serverjobs'),
    icon: <Turtle className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/chat/:chatId',
    element: ChatPage,
    label: i18n.t('navbar.chat'),
    showInNav: false,
    protected: true,
  },
  {
    path: '/chats',
    element: ChatsListPage,
    label: i18n.t('navbar.chats'),
    icon: <MessageCircleCode className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/users',
    element: UserPage,
    label: i18n.t('navbar.users'),
    icon: <User2Icon className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/search',
    element: SearchPage,
    label: i18n.t('navbar.search'),
    icon: <Search className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/exec',
    element: ExecPromptPage,
    label: i18n.t('navbar.prompt'),
    icon: <ChevronsRight className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/settings',
    element: () => <P>{i18n.t('navbar.settings')}</P>,
    label: i18n.t('navbar.settings'),
    icon: <Settings className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
  },
  {
    path: '/demo',
    element: BackendsPage,
    label: i18n.t('navbar.demo'),
    showInNav: false,
    protected: true,
  },
  {
    path: '/about',
    element: About,
    label: i18n.t('footer.about'),
    showInNav: false,
    protected: false,
  },
  {
    path: '/privacy',
    element: Privacy,
    label: i18n.t('footer.privacy'),
    showInNav: false,
    protected: false,
  },
  {
    path: LOGIN_ROUTE,
    element: AuthPage,
    label: i18n.t('login.title'),
    showInNav: false,
    protected: false,
  },
  {
    path: '/bye',
    element: ByePage,
    label: i18n.t('navbar.bye'),
    showInNav: false,
    system: true,
    protected: false,
  },
  {
    path: '*',
    element: () => i18n.t('pages.not_found'),
    label: i18n.t('pages.not_found'),
    showInNav: false,
    system: true,
    protected: false,
  },
];
