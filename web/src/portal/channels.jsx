import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import {
  Archive,
  ArrowDownWideNarrow,
  CirclePlus,
  Ellipsis,
  Eye,
  EyeOff,
  List,
  Plus,
  Pencil,
  Link2,
  Power,
  RefreshCw,
  SlidersHorizontal,
  Sparkles,
} from 'lucide-react'
import React, { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTablePagination } from '@/components/data-table/core/pagination'
import { useDataTableViewMode } from '@/components/data-table/hooks/use-data-table-view-mode'
import { DataTableCardGrid } from '@/components/data-table/layout/card-grid'
import { DataTableViewModeToggle } from '@/components/data-table/toolbar/view-mode-toggle'
import { PageFooterPortal } from '@/components/layout/components/page-footer'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import { api, channelStatus, formatTime, navigate, useResource } from './api'
import { CreateChannel } from './create-channel'
import {
  Page,
  Message,
  SelectBox,
  TableView,
  FormDialog,
  Field,
} from './shared'
import { parseModelNames, modelNameError } from './vertex-form'

const emptyRows = [],
  tableColumns = ['id', 'name', 'models', 'status', 'created_at'].map(
    (accessorKey) => ({ accessorKey })
  )
const stateOptions = [
  ['', '全部状态'],
  ['enabled', '已启用'],
  ['disabled', '已停用'],
  ['pending', '待确认'],
  ['rejected', '被拒绝'],
  ['archived', '已归档'],
]
function Filter({ title, value, options, change }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant='outline' className='border-dashed' />}
      >
        <CirclePlus />
        {title}
        {value && (
          <Badge variant='secondary'>
            {options.find((item) => item[0] === value)?.[1]}
          </Badge>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align='start'>
        {options.map(([key, label]) => (
          <DropdownMenuCheckboxItem
            key={key}
            checked={value === key}
            onClick={() => change(key)}
          >
            {label}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
export function Channels({ user, templates }) {
  const vertexTemplates = templates.filter((item) => item.type === 41)
  const [filters, setFilters] = useState({
      q: '',
      model: '',
      status: '',
      template: '',
      sort: 'latest',
    }),
    [search, setSearch] = useState(''),
    [modelSearch, setModelSearch] = useState('')
  const [paging, setPaging] = useState({ pageIndex: 0, pageSize: 20 }),
    [revision, refresh] = useState(0),
    [create, setCreate] = useState(false),
    [editor, setEditor] = useState(null),
    [binding, setBinding] = useState(null),
    [confirmation, setConfirmation] = useState(null),
    [busy, setBusy] = useState(false)
  const [visible, setVisible] = useState(true),
    [showModels, setShowModels] = useState(true),
    [showDates, setShowDates] = useState(true)
  const [view, setView] = useDataTableViewMode({
    storageKey: 'ali.channel.view',
    defaultMode: 'card',
  })
  function change(key, value) {
    setFilters((current) => ({ ...current, [key]: value }))
    setPaging((current) => ({ ...current, pageIndex: 0 }))
  }
  useEffect(() => {
    const timer = setTimeout(() => {
      setFilters((current) =>
        current.q === search && current.model === modelSearch
          ? current
          : { ...current, q: search, model: modelSearch }
      )
      setPaging((current) =>
        current.pageIndex === 0 ? current : { ...current, pageIndex: 0 }
      )
    }, 260)
    return () => clearTimeout(timer)
  }, [search, modelSearch])
  const query = new URLSearchParams({
    ...filters,
    page: paging.pageIndex + 1,
    page_size: paging.pageSize,
  })
  const result = useResource('channels?' + query, revision),
    rows = result.data?.items || emptyRows
  const table = useReactTable({
    data: rows,
    columns: tableColumns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => String(row.id),
    manualPagination: true,
    rowCount: result.data?.total || 0,
    state: { pagination: paging },
    onPaginationChange: setPaging,
  })
  const names = useMemo(
    () => Object.fromEntries(templates.map((item) => [item.id, item.label])),
    [templates]
  )
  const nameOf = (row) => (visible ? row.name : '••••'),
    typeOf = (row) => names[row.template_id] || row.template_id
  async function perform(row, action) {
    if (busy) return
    setBusy(true)
    try {
      if (action === 'archive') {
        await api(`channels/${row.id}/archive`, { method: 'POST', data: {} })
        if (rows.length === 1 && paging.pageIndex > 0) {
          setPaging((current) => ({
            ...current,
            pageIndex: Math.max(0, current.pageIndex - 1),
          }))
        }
        toast.success('失败记录已归档，可在「状态 → 已归档」查看')
        return
      }
      await api(
        `channels/${row.id}/${action === 'sync' ? 'sync' : 'lifecycle'}`,
        { method: 'POST', data: action === 'sync' ? {} : { action } }
      )
      toast.success(action === 'sync' ? '渠道状态已同步' : '渠道状态已更新')
    } catch (error) {
      toast.error(error.message)
    } finally {
      setBusy(false)
      setConfirmation(null)
      refresh((current) => current + 1)
    }
  }
  function actions(row) {
    if (row.state === 'archived')
      return <span className='text-muted-foreground text-xs'>只读</span>
    const ready = row.state === 'ready',
      action = row.status === 1 ? 'disable' : 'enable'
    return (
      <div className='flex items-center justify-end gap-1'>
        <Button
          variant='ghost'
          size='icon-xs'
          title='同步状态'
          aria-label='同步状态'
          disabled={busy || !ready}
          onClick={() => perform(row, 'sync')}
        >
          <RefreshCw />
        </Button>
        {ready && (
          <Button
            variant='ghost'
            size='icon-xs'
            title={action === 'disable' ? '停用' : '启用'}
            aria-label={action === 'disable' ? '停用' : '启用'}
            disabled={busy}
            onClick={() => setConfirmation({ row, action })}
          >
            <Power className={row.status === 1 ? 'text-success' : ''} />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button variant='ghost' size='icon-xs' aria-label='打开菜单' />
            }
          >
            <Ellipsis />
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end'>
            {ready && (
              <DropdownMenuItem onClick={() => setEditor(row)}>
                <Pencil />
                编辑渠道
              </DropdownMenuItem>
            )}
            {row.state === 'uncertain' && user.role === 'admin' && (
              <DropdownMenuItem onClick={() => setBinding(row)}>
                <Link2 />
                人工核对并绑定
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              disabled={!ready}
              onClick={() => navigate('/supplier/logs?channel=' + row.id)}
            >
              <List />
              查看调用日志
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={busy || !ready}
              onClick={() => perform(row, 'sync')}
            >
              <RefreshCw />
              同步状态
            </DropdownMenuItem>
            {row.can_archive && (
              <DropdownMenuItem
                disabled={busy}
                onClick={() => setConfirmation({ row, action: 'archive' })}
              >
                <Archive />
                归档失败记录
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    )
  }
  function card(row) {
    const label = 'text-muted-foreground text-[11px] font-medium'
    return (
      <div className='flex min-w-0 flex-col gap-3'>
        <div className='flex items-center justify-between gap-2'>
          <span className='flex min-w-0 items-center gap-2 truncate text-sm font-medium'>
            <Sparkles className='text-primary size-4 shrink-0' />
            {typeOf(row).split(' · ')[0]}
          </span>
          {actions(row)}
        </div>
        <div className='grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3'>
          <div>
            <div className={label}>#{visible ? row.id : '••••'}</div>
            <div className='text-sm font-medium break-words'>{nameOf(row)}</div>
          </div>
          <div>
            <div className={label}>状态</div>
            <span
              className={
                'text-xs ' +
                (row.state === 'ready' && row.status === 1
                  ? 'text-success'
                  : 'text-muted-foreground')
              }
            >
              {channelStatus(row)}
            </span>
          </div>
          <div>
            <div className={label}>账户</div>
            <span className='text-sm'>{visible ? row.username : '••••'}</span>
          </div>
          {showDates && (
            <div>
              <div className={label}>创建时间</div>
              <time className='text-xs tabular-nums'>
                {formatTime(row.created_at)}
              </time>
            </div>
          )}
        </div>
        {showModels && (
          <div className='flex flex-wrap gap-1'>
            {row.models
              .split(',')
              .filter(Boolean)
              .map((model) => (
                <Badge
                  key={model}
                  variant='secondary'
                  className='max-w-full truncate'
                >
                  {model}
                </Badge>
              ))}
          </div>
        )}
      </div>
    )
  }
  const columns = [
    ['id', 'ID', (row) => (visible ? `#${row.id}` : '••••')],
    ['name', '名称', nameOf],
    ['type', '类型', typeOf],
    ['status', '状态', channelStatus],
    ...(showModels ? [['models', '模型']] : []),
    ...(showDates
      ? [['created_at', '创建时间', (row) => formatTime(row.created_at)]]
      : []),
    ...(user.role === 'admin'
      ? [['username', '账户', (row) => (visible ? row.username : '••••')]]
      : []),
    ['actions', '操作', actions],
  ]
  return (
    <Page
      title='渠道'
      actions={
        <>
          <div className='flex items-center gap-1'>
            <ArrowDownWideNarrow className='text-muted-foreground size-4' />
            <SelectBox
              className='w-32'
              aria-label='渠道排序依据'
              value={filters.sort}
              onChange={(e) => change('sort', e.target.value)}
              options={[
                ['latest', '最新创建'],
                ['oldest', '最早创建'],
                ['name', '名称'],
                ['updated', '最近同步'],
              ]}
            />
          </div>
          <Button
            disabled={!vertexTemplates.length}
            onClick={() => setCreate(true)}
          >
            <Plus />
            创建渠道
          </Button>
        </>
      }
    >
      <div className='mb-3 flex flex-wrap items-center gap-2'>
        <Input
          aria-label='按名称或 ID 筛选'
          className='w-56 max-w-full'
          placeholder='按名称、ID 筛选…'
          value={search}
          maxLength={100}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Input
          aria-label='按模型筛选'
          className='w-40 max-w-full'
          placeholder='按模型筛选…'
          value={modelSearch}
          maxLength={100}
          onChange={(e) => setModelSearch(e.target.value)}
        />
        <Filter
          title='状态'
          options={stateOptions}
          value={filters.status}
          change={(value) => change('status', value)}
        />
        <Filter
          title='类型'
          options={[
            ['', '全部类型'],
            ...templates.map((item) => [item.id, item.label]),
          ]}
          value={filters.template}
          change={(value) => change('template', value)}
        />
        <div className='ms-auto flex items-center gap-2'>
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={visible ? '隐藏敏感信息' : '显示敏感信息'}
            title={visible ? '隐藏' : '显示'}
            onClick={() => setVisible(!visible)}
          >
            {visible ? <Eye /> : <EyeOff />}
          </Button>
          <DataTableViewModeToggle value={view} onChange={setView} />
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant='outline' />}>
              <SlidersHorizontal />
              查看
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuCheckboxItem
                checked={showModels}
                onCheckedChange={setShowModels}
              >
                模型
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                checked={showDates}
                onCheckedChange={setShowDates}
              >
                创建时间
              </DropdownMenuCheckboxItem>
              <DropdownMenuItem
                onClick={() => refresh((current) => current + 1)}
              >
                刷新列表
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      {result.error ? (
        <Message>{result.error}</Message>
      ) : view === 'card' ? (
        <DataTableCardGrid
          table={table}
          isLoading={result.loading}
          gridClassName='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-3'
          renderCard={(row) => card(row.original)}
          emptyTitle='暂无渠道'
          emptyDescription={
            filters.status === 'archived'
              ? '没有符合当前条件的已归档失败记录。'
              : '当前条件下没有渠道。可调整筛选条件，或点击「创建渠道」添加账号。'
          }
        />
      ) : (
        <TableView
          columns={columns}
          rows={rows}
          empty={result.loading ? '加载中…' : '暂无渠道'}
        />
      )}
      <PageFooterPortal>
        <DataTablePagination table={table} />
      </PageFooterPortal>
      {editor && (
        <FormDialog
          title='编辑渠道'
          description='保存后将按已绑定的渠道 ID 更新上游和本地的名称、模型。无需重新填写密钥。'
          close={() => setEditor(null)}
          save={async (fields) => {
            const name = fields.name.trim(),
              models = parseModelNames(fields.models)
            if (!name || [...name].length > 80)
              throw Error('请填写渠道名称（最多 80 字）')
            const message = modelNameError(models)
            if (message) throw Error(message)
            await api(`channels/${editor.id}`, {
              method: 'PUT',
              data: { name, models },
            })
            refresh((v) => v + 1)
            toast.success('渠道名称和模型已核对并保存')
          }}
        >
          <Field label='名称 *'>
            <Input
              name='name'
              defaultValue={editor.name}
              maxLength={80}
              required
            />
          </Field>
          <Field label='模型 *' hint='每行一个模型 ID，也可用逗号分隔。'>
            <Textarea
              name='models'
              defaultValue={editor.models.split(',').join('\n')}
              rows={4}
              required
              spellCheck={false}
            />
          </Field>
        </FormDialog>
      )}
      {binding && (
        <FormDialog
          title='人工核对并绑定'
          description={`本地 #${binding.id}「${binding.name}」，归属 ${binding.username}。请先在上游逐一核实归属；同名不是同一渠道的证明。`}
          submit='确认绑定'
          close={() => setBinding(null)}
          save={async (fields) => {
            await api(`admin/channels/${binding.id}/reconcile`, {
              method: 'POST',
              data: { upstream_id: Number(fields.upstream_id) },
            })
            refresh((v) => v + 1)
            toast.success('已绑定至原本地记录')
          }}
        >
          <Field label='已核实的上游渠道 ID'>
            <Input name='upstream_id' type='number' min='1' step='1' required />
          </Field>
        </FormDialog>
      )}
      {create && (
        <CreateChannel
          templates={vertexTemplates}
          close={() => setCreate(false)}
          saved={(row) => {
            setCreate(false)
            refresh((current) => current + 1)
            if (row.state === 'ready') toast.success('渠道已创建')
            else
              toast.warning(
                row.state === 'rejected'
                  ? '提交被拒，请检查配置'
                  : '提交结果待确认，请管理员按上游 ID 人工核对，不要重复上号'
              )
          }}
        />
      )}
      {confirmation && (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && !busy && setConfirmation(null)}
          title={
            confirmation.action === 'archive'
              ? '归档失败记录？'
              : confirmation.action === 'disable'
                ? '停用渠道？'
                : '启用渠道？'
          }
          desc={
            confirmation.action === 'archive'
              ? `「${nameOf(confirmation.row)}」将从默认列表移走，可在「状态 → 已归档」中查看。审计记录会保留，不会操作上游渠道。`
              : '此操作将变更该渠道在号池中的实际状态。'
          }
          destructive={confirmation.action === 'disable'}
          isLoading={busy}
          confirmText={confirmation.action === 'archive' ? '确认归档' : '确认'}
          handleConfirm={() => perform(confirmation.row, confirmation.action)}
        />
      )}
    </Page>
  )
}
