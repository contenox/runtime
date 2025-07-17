import { P } from '@contenox/ui';
import {
  ChevronsRight,
  Database,
  File,
  Home,
  Link,
  MessageCircleCode,
  Search,
  Settings,
  Turtle,
  User2Icon,
} from 'lucide-react';
import i18n from '../i18n';
import BackendsPage from '../pages/admin/backends/BackendPage.tsx';
import ChainsPage from '../pages/admin/chains/ChainsPage.tsx';
import ChainDetailPage from '../pages/admin/chains/components/ChainDetailPage.tsx';
import ChatPage from '../pages/admin/chats/ChatPage.tsx';
import ChatsListPage from '../pages/admin/chats/components/ChatListPage.tsx';
import FilesPage from '../pages/admin/files/FilesPage.tsx';
import GitHubPage from '../pages/admin/github/GitHubPage.tsx';
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
  showInShelf?: boolean;
}

export const routes: RouteConfig[] = [
  {
    path: '/',
    element: HomePage,
    label: i18n.t('navbar.home'),
    icon: <Home className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: false,
    showInShelf: true,
  },
  {
    path: '/backends',
    element: BackendsPage,
    label: i18n.t('navbar.backends'),
    icon: <Database className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/files',
    element: FilesPage,
    label: i18n.t('navbar.files'),
    icon: <File className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/github',
    element: GitHubPage,
    label: i18n.t('navbar.github'),
    icon: <Link className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/jobs',
    element: ServerJobsPage,
    label: i18n.t('navbar.serverjobs'),
    icon: <Turtle className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/chat/:chatId',
    element: ChatPage,
    label: i18n.t('navbar.chat'),
    showInNav: false,
    protected: true,
    showInShelf: false,
  },

  {
    path: '/chats',
    element: ChatsListPage,
    label: i18n.t('navbar.chats'),
    icon: <MessageCircleCode className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/users',
    element: UserPage,
    label: i18n.t('navbar.users'),
    icon: <User2Icon className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/search',
    element: SearchPage,
    label: i18n.t('navbar.search'),
    icon: <Search className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/chains',
    element: ChainsPage,
    label: i18n.t('navbar.chains'),
    icon: <Link className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/chains/:id',
    element: ChainDetailPage,
    label: i18n.t('navbar.chain_detail'),
    showInNav: false,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/exec',
    element: ExecPromptPage,
    label: i18n.t('navbar.prompt'),
    icon: <ChevronsRight className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/settings',
    element: () => <P>{i18n.t('navbar.settings')}</P>,
    label: i18n.t('navbar.settings'),
    icon: <Settings className="h-[1em] w-[1em]" />,
    showInNav: true,
    protected: true,
    showInShelf: false,
  },
  {
    path: '/about',
    element: About,
    label: i18n.t('footer.about'),
    showInNav: false,
    protected: false,
    showInShelf: true,
  },
  {
    path: '/privacy',
    element: Privacy,
    label: i18n.t('footer.privacy'),
    showInNav: false,
    protected: false,
    showInShelf: true,
  },
  {
    path: LOGIN_ROUTE,
    element: AuthPage,
    label: i18n.t('login.title'),
    showInNav: false,
    protected: false,
    showInShelf: false,
  },
  {
    path: '/bye',
    element: ByePage,
    label: i18n.t('navbar.bye'),
    showInNav: false,
    system: true,
    protected: false,
    showInShelf: false,
  },
  {
    path: '*',
    element: () => i18n.t('pages.not_found'),
    label: i18n.t('pages.not_found'),
    showInNav: false,
    system: true,
    protected: false,
    showInShelf: false,
  },
];
