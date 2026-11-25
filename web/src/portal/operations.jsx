import { Link2, Plus, RefreshCw } from 'lucide-react'
import React, { useState } from 'react'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { PageFooterPortal } from '@/components/layout/components/page-footer'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { api, formatTime, navigate, useResource } from './api'
import {
  Field,
  FormDialog,
  Message,
  Page,
  SelectBox,
  TableView,
} from './shared'

const statuses = {
  success: '成功',
  invalid_usage: '用量异常',
  stream_interrupted: '流式中断',
  downstream_dropped: '客户端断开',
  local_failure: '处理失败',
  provider_rejected: '请求被拒',
  network_error: '网络异常',
  protocol_error: '协议异常',
}
export function Logs({ query }) {
  const selected = new URLSearchParams(query).get('channel') || ''
  const [days, setDays] = useState('1'),
    [page, setPage] = useState(1),
    [channelPage, setChannelPage] = useState(1),
    [rev, refresh] = useState(0)
  const list = useResource(`channels?page=${channelPage}&page_size=100`),
    options = (list.data?.items || [])
      .filter((item) => item.state === 'ready')
      .map((item) => [String(item.id), item.name])
  if (selected && !options.some((item) => item[0] === selected))
    options.unshift([selected, `渠道 #${selected}`])
  const result = useResource(
    selected
      ? `channels/${encodeURIComponent(selected)}/logs?days=${days}&page=${page}`
      : null,
    rev
  )
  return (
    <Page
      title='调用日志'
      actions={
        <Button variant='outline' onClick={() => refresh((v) => v + 1)}>
          <RefreshCw />
          刷新
        </Button>
      }
    >
      <div className='mb-4 flex flex-wrap items-end gap-3'>
        <Field label='渠道'>
          <SelectBox
            className='w-52'
            options={[['', '请选择渠道'], ...options]}
            value={selected}
            onChange={(e) => {
              setPage(1)
              navigate(
                '/supplier/logs' +
                  (e.target.value ? '?channel=' + e.target.value : '')
              )
            }}
          />
        </Field>
        {list.data?.total > 100 && (
          <Field label='渠道列表页'>
            <SelectBox
              value={channelPage}
              onChange={(e) => setChannelPage(Number(e.target.value))}
              options={Array.from(
                { length: Math.ceil(list.data.total / 100) },
                (_, index) => [index + 1, `第 ${index + 1} 页`]
              )}
            />
          </Field>
        )}
        <Field label='时间范围'>
          <SelectBox
            className='w-36'
            value={days}
            options={[
              ['1', '最近 24 小时'],
              ['7', '最近 7 天'],
              ['30', '最近 30 天'],
            ]}
            onChange={(e) => {
              setPage(1)
              setDays(e.target.value)
            }}
          />
        </Field>
      </div>
      <Message>{list.error || result.error}</Message>
      <TableView
        rows={result.data?.items}
        empty={
          selected
            ? result.loading
              ? '加载中…'
              : '暂无调用记录'
            : '请选择渠道查看调用日志'
        }
        columns={[
          ['created_at', '时间', (r) => formatTime(r.created_at)],
          ['model_name', '模型'],
          [
            'attempt_status',
            '状态',
            (r) => statuses[r.attempt_status] || '未知',
          ],
          ['prompt_tokens', '输入 Token'],
          ['completion_tokens', '输出 Token'],
          ['is_stream', '流式', (r) => (r.is_stream ? '是' : '否')],
        ]}
      />
      <PageFooterPortal>
        <div className='flex items-center justify-end gap-3 text-sm'>
          <span>总计：{result.data?.total ?? '—'}</span>
          <Button
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage(page - 1)}
          >
            上一页
          </Button>
          <span>{page}</span>
          <Button
            variant='outline'
            disabled={!result.data || page * 50 >= result.data.total}
            onClick={() => setPage(page + 1)}
          >
            下一页
          </Button>
        </div>
      </PageFooterPortal>
    </Page>
  )
}
export function Customers({ templates }) {
  const [rev, reload] = useState(0),
    [dialog, setDialog] = useState(''),
    [target, setTarget] = useState(null),
    [busy, setBusy] = useState(false)
  const result = useResource('admin/users', rev),
    rows = result.data || []
  async function connection() {
    setBusy(true)
    try {
      const data = await api('admin/connection')
      if (data.channel_read && data.channel_write && data.usage_read)
        toast.success('渠道读写和日志权限正常')
      else toast.warning('连接成功，但权限不完整')
    } catch (error) {
      toast.error(error.message)
    } finally {
      setBusy(false)
    }
  }
  async function update() {
    setBusy(true)
    try {
      await api('admin/users/' + target.id, {
        method: 'PATCH',
        data: { enabled: !target.enabled },
      })
      reload((v) => v + 1)
      setTarget(null)
      toast.success('客户状态已更新')
    } catch (error) {
      toast.error(error.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Page
      title='客户管理'
      actions={
        <>
          <Button variant='outline' disabled={busy} onClick={connection}>
            <RefreshCw />
            检查连接
          </Button>
          <Button
            variant='outline'
            disabled={!templates.length || !rows.length}
            onClick={() => setDialog('bind')}
          >
            <Link2 />
            绑定已有渠道
          </Button>
          <Button onClick={() => setDialog('user')}>
            <Plus />
            新增客户
          </Button>
        </>
      }
    >
      <Message>{result.error}</Message>
      <TableView
        rows={rows}
        columns={[
          ['id', 'ID'],
          ['username', '账号'],
          ['role', '角色', (r) => (r.role === 'admin' ? '管理员' : '客户')],
          ['enabled', '状态', (r) => (r.enabled ? '可登录' : '已停用')],
          [
            'action',
            '操作',
            (r) =>
              r.role === 'admin' ? (
                '—'
              ) : (
                <Button variant='ghost' size='sm' onClick={() => setTarget(r)}>
                  {r.enabled ? '停用' : '启用'}
                </Button>
              ),
          ],
        ]}
      />
      {dialog === 'user' && (
        <FormDialog
          title='新增客户'
          description='为客户开通独立的本地登录账号。'
          submit='创建客户'
          close={() => setDialog('')}
          save={async (data) => {
            await api('admin/users', { method: 'POST', data })
            reload((v) => v + 1)
            toast.success('客户已创建')
          }}
        >
          <Field label='用户名'>
            <Input
              name='username'
              required
              pattern='[a-zA-Z0-9_\-]{3,40}'
              autoComplete='off'
            />
          </Field>
          <Field label='初始密码' hint='15–128 字符，请通过安全渠道交付。'>
            <PasswordInput
              name='password'
              required
              minLength={15}
              maxLength={128}
              autoComplete='new-password'
            />
          </Field>
        </FormDialog>
      )}
      {dialog === 'bind' && (
        <FormDialog
          title='绑定已有渠道'
          description='绑定后该客户可查看此渠道的日志并管理启停，请先核实归属。'
          submit='核验并绑定'
          close={() => setDialog('')}
          save={async (data) => {
            await api('admin/bind', {
              method: 'POST',
              data: {
                ...data,
                user_id: Number(data.user_id),
                upstream_id: Number(data.upstream_id),
              },
            })
            toast.success('渠道已绑定')
          }}
        >
          <Field label='客户'>
            <SelectBox
              name='user_id'
              required
              options={rows
                .filter((r) => r.enabled)
                .map((r) => [r.id, r.username])}
            />
          </Field>
          <Field label='上游渠道 ID'>
            <Input name='upstream_id' type='number' required min='1' step='1' />
          </Field>
          <Field label='展示名称'>
            <Input name='name' required maxLength={80} />
          </Field>
          <Field label='类型'>
            <SelectBox
              name='template_id'
              options={templates.map((r) => [r.id, r.label])}
            />
          </Field>
        </FormDialog>
      )}
      {target && (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && !busy && setTarget(null)}
          title={(target.enabled ? '停用' : '启用') + '客户账号？'}
          desc='此操作会撤销该账号的现有登录，不改变其渠道状态。'
          destructive={target.enabled}
          isLoading={busy}
          confirmText='确认'
          handleConfirm={update}
        />
      )}
    </Page>
  )
}
export function Audit() {
  const [rev, reload] = useState(0),
    result = useResource('admin/audit', rev)
  return (
    <Page
      title='审计日志'
      actions={
        <Button variant='outline' onClick={() => reload((v) => v + 1)}>
          <RefreshCw />
          刷新
        </Button>
      }
    >
      <p className='text-muted-foreground mb-3 text-sm'>
        最近 100 条本地工作台操作记录
      </p>
      <Message>{result.error}</Message>
      <TableView
        rows={result.data}
        columns={[
          ['created_at', '时间', (r) => formatTime(r.created_at)],
          ['user_id', '用户 ID', (r) => r.user_id || '—'],
          ['action', '操作'],
          ['channel_id', '渠道 ID', (r) => r.channel_id || '—'],
          ['outcome', '结果'],
        ]}
      />
    </Page>
  )
}
export function Security({ user, signedOut }) {
  const [open, setOpen] = useState(false)
  return (
    <Page title='安全与访问'>
      <div className='flex max-w-xl items-center justify-between gap-3 rounded-lg border p-5'>
        <div>
          <h3 className='font-medium'>登录密码</h3>
          <p className='text-muted-foreground text-sm'>
            {user.username} · 修改后所有设备需重新登录
          </p>
        </div>
        <Button variant='outline' onClick={() => setOpen(true)}>
          修改密码
        </Button>
      </div>
      {open && (
        <FormDialog
          title='修改密码'
          description='请输入当前密码及 15–128 字符的新密码。'
          close={() => setOpen(false)}
          save={async (data) => {
            await api('password', { method: 'POST', data })
            signedOut()
            toast.success('密码已修改，请重新登录')
          }}
        >
          <Field label='当前密码'>
            <PasswordInput
              name='current_password'
              autoComplete='current-password'
              required
            />
          </Field>
          <Field label='新密码'>
            <PasswordInput
              name='new_password'
              autoComplete='new-password'
              required
              minLength={15}
              maxLength={128}
            />
          </Field>
        </FormDialog>
      )}
    </Page>
  )
}
