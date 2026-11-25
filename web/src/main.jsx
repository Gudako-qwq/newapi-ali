import i18next from 'i18next'
import {
  Activity,
  ClipboardList,
  FileClock,
  KeyRound,
  LogOut,
  Moon,
  Palette,
  Search,
  Sun,
  Users,
} from 'lucide-react'
import React, { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { initReactI18next } from 'react-i18next'
import { Toaster, toast } from 'sonner'

import { Header } from '@/components/layout/components/header'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  SidebarProvider,
  Sidebar,
  SidebarContent,
  SidebarInset,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthLayout } from '@/features/auth/auth-layout'

import {
  api,
  navigate,
  paths,
  safeDestination,
  setSession,
  useResource,
} from './portal/api'
import { Channels } from './portal/channels'
import { Audit, Customers, Logs, Security } from './portal/operations'
import { Field, Message } from './portal/shared'

import './styles/index.css'

i18next.use(initReactI18next).init({
  lng: 'zh',
  fallbackLng: 'zh',
  initImmediate: false,
  keySeparator: false,
  interpolation: { escapeValue: false },
  resources: {
    zh: {
      translation: {
        Logo: '徽标',
        Cancel: '取消',
        Continue: '确认',
        Authentication: '身份验证',
        'Click to copy': '点击复制',
        'Copied: {{model}}': '已复制：{{model}}',
        'Failed to copy': '复制失败',
        'Show All': '显示全部',
        Collapse: '收起',
        'Basic Information': '基本信息',
        'Name, provider type, and availability.':
          '名称、供应商类型和可用状态。',
        Credentials: '凭证',
        'Models & Groups': '模型与分组',
        'Published models, groups, and model remapping rules.':
          '填写此渠道支持的模型。',
        'Card view': '卡片视图',
        'Table view': '表格视图',
        'View mode': '视图模式',
        'No Data': '暂无数据',
        'No data available': '暂无记录',
        Page: '页',
        'Total:': '总计：',
        'Rows per page': '每页行数',
        'Go to previous page': '前往上一页',
        'Go to next page': '前往下一页',
        'Go to first page': '前往首页',
        'Go to last page': '前往末页',
        'Go to page {{page}}': '前往第 {{page}} 页',
      },
    },
  },
})

const navItems = [
  ['/channels', '渠道', Activity, '供应商控制台'],
  ['/supplier/logs', '调用日志', FileClock, '供应商控制台'],
  ['/customers', '客户管理', Users, '管理'],
  ['/audit', '审计日志', ClipboardList, '管理'],
  ['/security', '安全与访问', KeyRound, '个人'],
]
function SignIn({ success }) {
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  async function submit(event) {
    event.preventDefault()
    if (busy) return
    const data = Object.fromEntries(new FormData(event.currentTarget))
    setBusy(true)
    setError('')
    try {
      success(await api('login', { method: 'POST', data }))
    } catch (reason) {
      setError(reason.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <AuthLayout>
      <div className='w-full space-y-8'>
        <h2 className='text-center text-2xl font-semibold tracking-tight sm:text-left'>
          登录
        </h2>
        <form className='grid gap-5' onSubmit={submit}>
          <Field label='用户名'>
            <Input
              required
              name='username'
              autoComplete='username'
              placeholder='请输入用户名'
            />
          </Field>
          <Field label='密码'>
            <PasswordInput
              name='password'
              required
              autoComplete='current-password'
              placeholder='请输入密码'
            />
          </Field>
          <Message>{error}</Message>
          <Button className='w-full' type='submit' disabled={busy}>
            {busy ? '正在登录…' : '登录'}
          </Button>
        </form>
      </div>
    </AuthLayout>
  )
}
function Navigation({ items, path }) {
  const { setOpenMobile } = useSidebar()
  return (
    <Sidebar variant='inset' collapsible='icon'>
      <SidebarContent>
        {[...new Set(items.map((item) => item[3]))].map((group) => (
          <SidebarGroup key={group}>
            <SidebarGroupLabel>{group}</SidebarGroupLabel>
            <SidebarMenu>
              {items
                .filter((item) => item[3] === group)
                .map(([url, title, Icon]) => (
                  <SidebarMenuItem key={url}>
                    <SidebarMenuButton
                      isActive={url === path}
                      tooltip={title}
                      render={<a href={url} />}
                      onClick={(event) => {
                        if (!event.ctrlKey && !event.metaKey) {
                          event.preventDefault()
                          setOpenMobile(false)
                          navigate(url)
                        }
                      }}
                    >
                      <Icon />
                      <span>{title}</span>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
            </SidebarMenu>
          </SidebarGroup>
        ))}
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  )
}
function Console({ user, path, query, signedOut }) {
  const [about, setAbout] = useState(false)
  const [search, setSearch] = useState(false),
    [term, setTerm] = useState(''),
    [dark, setDark] = useState(() =>
      document.documentElement.classList.contains('dark')
    )
  const [rev, refresh] = useState(0),
    catalog = useResource('templates', rev)
  const items = navItems.filter(
    (item) => user.role === 'admin' || item[3] !== '管理'
  )
  useEffect(() => {
    const handle = (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setSearch((open) => !open)
      }
    }
    window.addEventListener('keydown', handle)
    return () => window.removeEventListener('keydown', handle)
  }, [])
  function theme(value) {
    document.documentElement.classList.toggle('dark', value)
    setDark(value)
    try {
      localStorage.setItem('ali.theme', value ? 'dark' : 'light')
    } catch {}
  }
  async function logout() {
    try {
      await api('logout', { method: 'POST' })
      signedOut()
    } catch (error) {
      toast.error(error.message)
    }
  }
  return (
    <SidebarProvider className='flex-col'>
      <Header>
        <a
          href='/channels'
          className='flex items-center gap-2 text-sm font-semibold'
        >
          <img src='/logo.png' alt='' className='size-6 rounded-full' />
          New API
        </a>
        <div className='ms-auto flex items-center gap-1 sm:gap-2'>
          <Button
            variant='ghost'
            className='hidden sm:inline-flex'
            onClick={() => navigate('/channels')}
          >
            控制台
          </Button>
          <Button
            variant='outline'
            className='text-muted-foreground justify-start gap-2 sm:w-44'
            onClick={() => {
              setTerm('')
              setSearch(true)
            }}
          >
            <Search />
            搜索 <kbd className='ms-auto hidden text-xs sm:inline'>⌘ K</kbd>
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button variant='ghost' size='icon' aria-label='打开主题设置' />
              }
            >
              <Palette />
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuItem onClick={() => theme(false)}>
                <Sun />
                浅色{!dark ? ' ✓' : ''}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => theme(true)}>
                <Moon />
                深色{dark ? ' ✓' : ''}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  size='icon-sm'
                  className='rounded-full'
                  aria-label='账号菜单'
                />
              }
            >
              {user.username[0].toUpperCase()}
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuItem disabled>{user.username}</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setAbout(true)}>
                关于 New API
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => navigate('/security')}>
                <KeyRound />
                安全与访问
              </DropdownMenuItem>
              <DropdownMenuItem onClick={logout}>
                <LogOut />
                退出登录
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </Header>
      <div className='flex min-h-0 w-full flex-1'>
        <Navigation items={items} path={path} />
        <SidebarInset className='@container/content h-[calc(100svh-var(--app-header-height))] min-h-0 overflow-hidden peer-data-[variant=inset]:h-[calc(100svh-var(--app-header-height)-(var(--spacing)*4))]'>
          {catalog.error && (
            <div className='p-3'>
              <Message>{catalog.error}</Message>
              <Button variant='outline' onClick={() => refresh((v) => v + 1)}>
                重试加载模板
              </Button>
            </div>
          )}
          {path === '/channels' ? (
            <Channels user={user} templates={catalog.data || []} />
          ) : path === '/supplier/logs' ? (
            <Logs query={query} />
          ) : path === '/customers' && user.role === 'admin' ? (
            <Customers templates={catalog.data || []} />
          ) : path === '/audit' && user.role === 'admin' ? (
            <Audit />
          ) : path === '/security' ? (
            <Security user={user} signedOut={signedOut} />
          ) : (
            <div className='p-6'>
              无权访问本页。
              <Button variant='link' onClick={() => navigate('/channels')}>
                返回渠道
              </Button>
            </div>
          )}
        </SidebarInset>
      </div>
      <Dialog open={about} onOpenChange={setAbout}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>关于 New API</DialogTitle>
            <DialogDescription>
              基于 New API 1.0.0-rc.50 前端二次开发，使用独立的渠道与日志服务。
            </DialogDescription>
          </DialogHeader>
          <p className='text-sm'>
            Frontend design and development by New API contributors.
          </p>
          <div className='text-primary flex flex-wrap gap-4 text-sm underline'>
            <a
              target='_blank'
              rel='noreferrer'
              href='https://github.com/QuantumNous/new-api'
            >
              New API 原项目
            </a>
            <a target='_blank' rel='noreferrer' href='/LICENSE'>
              许可证
            </a>
            <a target='_blank' rel='noreferrer' href='/NOTICE'>
              版权声明
            </a>
          </div>
        </DialogContent>
      </Dialog>
      <Dialog open={search} onOpenChange={setSearch}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>搜索</DialogTitle>
            <DialogDescription>查找并前往工作台页面。</DialogDescription>
          </DialogHeader>
          <Input
            aria-label='搜索页面'
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            placeholder='输入页面名称…'
          />
          <div className='grid gap-1'>
            {items
              .filter((item) => item[1].includes(term))
              .map(([url, title, Icon]) => (
                <Button
                  key={url}
                  variant='ghost'
                  className='justify-start'
                  onClick={() => {
                    setSearch(false)
                    navigate(url)
                  }}
                >
                  <Icon />
                  {title}
                </Button>
              ))}
            {!items.some((item) => item[1].includes(term)) && (
              <p className='text-muted-foreground py-3 text-center text-sm'>
                未找到页面
              </p>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </SidebarProvider>
  )
}
function App() {
  const [user, setUser] = useState(undefined),
    [bootError, setBootError] = useState(''),
    [rev, reload] = useState(0),
    [url, setURL] = useState(() => new URL(window.location.href))
  function accept(value) {
    setSession(value)
    setUser(value?.user || null)
  }
  function signOut() {
    accept(null)
    navigate('/sign-in', true)
  }
  useEffect(() => {
    const update = () => setURL(new URL(window.location.href))
    const expire = () => {
      accept(null)
      navigate(
        '/sign-in?redirect=' +
          encodeURIComponent(
            safeDestination(window.location.pathname + window.location.search)
          ),
        true
      )
    }
    window.addEventListener('popstate', update)
    window.addEventListener('session-expired', expire)
    return () => {
      window.removeEventListener('popstate', update)
      window.removeEventListener('session-expired', expire)
    }
  }, [])
  useEffect(() => {
    let alive = true
    setBootError('')
    api('session')
      .then((data) => {
        if (alive) accept(data)
      })
      .catch((error) => {
        if (!alive) return
        if (error.status === 401) accept(null)
        else setBootError(error.message)
      })
    return () => {
      alive = false
    }
  }, [rev])
  const redirect = safeDestination(url.searchParams.get('redirect'))
  useEffect(() => {
    if (user === undefined) return
    if (!user && url.pathname !== '/sign-in') {
      navigate(
        '/sign-in?redirect=' +
          encodeURIComponent(safeDestination(url.pathname + url.search)),
        true
      )
    } else if (user && !paths.includes(url.pathname)) {
      navigate(redirect, true)
    }
  }, [user, url, redirect])
  if (bootError)
    return (
      <div className='m-6'>
        <Message>{bootError}</Message>
        <Button onClick={() => reload((v) => v + 1)}>重新连接</Button>
      </div>
    )
  if (user === undefined)
    return (
      <div className='text-muted-foreground grid h-svh place-items-center text-sm'>
        加载中…
      </div>
    )
  return user ? (
    <Console
      key={user.id}
      user={user}
      path={paths.includes(url.pathname) ? url.pathname : '/channels'}
      query={url.search}
      signedOut={signOut}
    />
  ) : (
    <SignIn
      success={(data) => {
        accept(data)
        navigate(redirect, true)
      }}
    />
  )
}
try {
  document.documentElement.classList.toggle(
    'dark',
    localStorage.getItem('ali.theme') === 'dark'
  )
} catch {}
createRoot(document.getElementById('root')).render(
  <TooltipProvider>
    <App />
    <Toaster richColors />
  </TooltipProvider>
)
