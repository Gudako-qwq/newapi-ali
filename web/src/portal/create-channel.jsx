import { Boxes, CheckCircle2, Circle, KeyRound, Server } from 'lucide-react'
import React, { useId, useRef, useState } from 'react'

import {
  sideDrawerContentClassName,
  sideDrawerHeaderClassName,
  sideDrawerFooterClassName,
} from '@/components/drawer-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectGroup,
  SelectItem,
} from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetFooter,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { ChannelApiAccessSection } from '@/features/channels/components/drawers/sections/channel-api-access-section'
import { ChannelAuthSection } from '@/features/channels/components/drawers/sections/channel-auth-section'
import { ChannelBasicSection } from '@/features/channels/components/drawers/sections/channel-basic-section'
import { ChannelModelsSection } from '@/features/channels/components/drawers/sections/channel-models-section'

import { api } from './api'
import { Message } from './shared'
import {
  defaultVertexTemplate,
  parseModelNames,
  validateVertex,
} from './vertex-form'

const cardClass = 'border-border/60 bg-muted/10 space-y-4 rounded-lg border p-4'
const descriptionClass = 'text-muted-foreground text-sm'
const sections = [
  ['基本信息', Server, ['type', 'name']],
  ['凭证', KeyRound, ['region', 'project', 'credential']],
  ['模型与分组', Boxes, ['models']],
]

function VertexMark() {
  const gradient = useId()
  return (
    <svg viewBox='0 0 24 24' className='size-5 shrink-0' aria-hidden='true'>
      <defs>
        <linearGradient id={gradient} x1='0' y1='1' x2='1' y2='0'>
          <stop stopColor='#34a853' />
          <stop offset='.28' stopColor='#4285f4' />
          <stop offset='.54' stopColor='#4285f4' />
          <stop offset='.76' stopColor='#ea4335' />
          <stop offset='1' stopColor='#fbbc04' />
        </linearGradient>
      </defs>
      <path
        fill={`url(#${gradient})`}
        d='M12 0C13.2 6.2 17.8 10.8 24 12c-6.2 1.2-10.8 5.8-12 12C10.8 17.8 6.2 13.2 0 12 6.2 10.8 10.8 6.2 12 0Z'
      />
    </svg>
  )
}
const providerOptions = [
  { value: '41', label: 'Vertex AI', icon: <VertexMark /> },
]

function Picker({ id, value, items, onChange, disabled, className }) {
  return (
    <Select
      value={value}
      items={items}
      disabled={disabled}
      onValueChange={(next) => {
        if (next != null) onChange?.(next)
      }}
    >
      <SelectTrigger id={id} className={className}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}
function InlineError({ id, message }) {
  return message ? (
    <p id={id} role='alert' className='text-destructive text-xs'>
      {message}
    </p>
  ) : null
}

export function CreateChannel({ templates, close, saved }) {
  const [kind, setKind] = useState(() => defaultVertexTemplate(templates).id)
  const template = templates.find((item) => item.id === kind)
  const isKey = template.credential_kind === 'api_key'
  const [name, setName] = useState(''),
    [credential, setCredential] = useState(''),
    [project, setProject] = useState(''),
    [modelText, setModelText] = useState('')
  const models = parseModelNames(modelText)
  const [region, setRegion] = useState(() => template.default_region),
    [active, setActive] = useState(0),
    [attempted, setAttempted] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const form = useRef(null),
    nodes = useRef([]),
    sequence = useRef(0),
    attempt = useRef({})
  const errors = validateVertex({
    template,
    name,
    credential,
    project,
    region,
    models,
  })
  const complete = sections.map(
    ([, , fields]) => !fields.some((field) => errors[field])
  )
  const control = (field) => ({
    id: `vertex-${field}`,
    'aria-invalid': attempted && !!errors[field],
    'aria-describedby':
      attempted && errors[field] ? `vertex-error-${field}` : undefined,
  })
  const feedback = (field) => (
    <InlineError
      id={`vertex-error-${field}`}
      message={attempted && errors[field]}
    />
  )
  const formatItems = templates.map((item) => ({
    value: item.id,
    label:
      (item.credential_kind === 'api_key' ? 'API 密钥' : 'JSON') +
      (templates.some(
        (other) =>
          other.id !== item.id && other.credential_kind === item.credential_kind
      )
        ? ` · ${item.label}`
        : ''),
  }))
  function chooseFormat(value) {
    if (value === kind) return
    const next = templates.find((item) => item.id === value)
    if (!next) return
    sequence.current++
    setKind(value)
    setCredential('')
    setProject('')
    setError('')
    attempt.current = {}
    setRegion((current) =>
      current === template.default_region || !next.regions.includes(current)
        ? next.default_region
        : current
    )
  }
  function jump(index) {
    setActive(index)
    nodes.current[index]?.scrollIntoView({ block: 'start' })
  }
  function trackSection(event) {
    const view = event.currentTarget,
      top = view.getBoundingClientRect().top + 36
    if (
      view.scrollTop > 0 &&
      view.scrollTop + view.clientHeight >= view.scrollHeight - 2
    ) {
      setActive(2)
      return
    }
    let current = 0
    nodes.current.forEach((node, index) => {
      if (node && node.getBoundingClientRect().top <= top) current = index
    })
    setActive(current)
  }
  function section(index, content) {
    return (
      <div
        className='scroll-mt-4'
        ref={(node) => {
          nodes.current[index] = node
        }}
        onFocusCapture={() => setActive(index)}
      >
        {content}
      </div>
    )
  }
  async function loadFile(event) {
    const file = event.target.files[0],
      version = ++sequence.current
    event.target.value = ''
    if (!file) return
    try {
      if (file.size > 131072) throw Error()
      const text = await file.text()
      JSON.parse(text)
      if (version === sequence.current) {
        setCredential(text)
        setError('')
      }
    } catch {
      if (version === sequence.current)
        setError('请选择有效的服务账号 JSON 文件（最多 128 KB）。')
    }
  }
  async function create(event) {
    event.preventDefault()
    if (busy) return
    setAttempted(true)
    setError('')
    const invalid = sections.findIndex(([, , fields]) =>
      fields.some((field) => errors[field])
    )
    if (invalid >= 0) {
      jump(invalid)
      const field = sections[invalid][2].find((field) => errors[field])
      form.current?.querySelector(`#vertex-${field}`)?.focus()
      return
    }
    const data = {
      template_id: kind,
      name: name.trim(),
      credential: credential.trim(),
      region: region.trim(),
      project_id: isKey ? project.trim() : '',
      models,
    }
    const body = JSON.stringify(data)
    if (attempt.current.body !== body)
      attempt.current = { body, key: crypto.randomUUID() }
    setBusy(true)
    try {
      saved(
        await api('channels', {
          method: 'POST',
          data,
          headers: { 'Idempotency-Key': attempt.current.key },
        })
      )
    } catch (reason) {
      setError(reason.message + '。若网络中断，请先核对列表，不要重复上号。')
    } finally {
      setBusy(false)
    }
  }
  const formatLabel = isKey ? 'API 密钥' : '服务账号 JSON'
  return (
    <Sheet open onOpenChange={(open) => !open && !busy && close()}>
      <SheetContent
        className={sideDrawerContentClassName('sm:max-w-[64rem]')}
        showCloseButton={!busy}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle className='flex items-center gap-2'>
            <span className='bg-primary/10 grid size-8 place-items-center rounded-full'>
              <VertexMark />
            </span>
            创建渠道{' '}
            <span className='text-muted-foreground text-sm font-normal'>
              Vertex AI
            </span>
          </SheetTitle>
          <SheetDescription>通过提供必要信息添加新的渠道。</SheetDescription>
        </SheetHeader>
        <div className='flex min-h-0 flex-1 overflow-hidden'>
          <aside className='hidden w-56 shrink-0 space-y-3 overflow-auto p-5 pe-0 lg:block'>
            <div className='bg-muted/10 flex items-center gap-3 rounded-lg border p-3 text-sm font-medium'>
              <span className='bg-background grid size-8 place-items-center rounded-lg border'>
                <VertexMark />
              </span>
              <div>
                Vertex AI
                <p className='text-muted-foreground text-xs font-normal'>
                  已启用 {complete.filter(Boolean).length}/3
                </p>
              </div>
            </div>
            <nav aria-label='渠道表单分区' className='rounded-lg border p-1'>
              {sections.map(([label, Icon], index) => (
                <button
                  key={label}
                  type='button'
                  aria-current={active === index ? 'step' : undefined}
                  onClick={() => jump(index)}
                  className={
                    'flex w-full items-center gap-3 rounded-md p-2 text-left text-sm ' +
                    (active === index ? 'bg-muted/70' : '')
                  }
                >
                  <span className='bg-muted/60 grid size-7 place-items-center rounded-md'>
                    <Icon className='size-4' />
                  </span>
                  <span className='flex-1 font-medium'>
                    {label}
                    <small className='text-muted-foreground block text-xs font-normal'>
                      {complete[index] ? '就绪' : '未完成'}
                    </small>
                  </span>
                  {complete[index] ? (
                    <CheckCircle2 className='text-primary size-3.5' />
                  ) : (
                    <Circle className='text-muted-foreground size-3.5' />
                  )}
                </button>
              ))}
            </nav>
          </aside>
          <form
            ref={form}
            id='channel-form'
            noValidate
            onSubmit={create}
            onScroll={trackSection}
            className='min-w-0 flex-1 overflow-y-auto p-4 sm:p-6'
          >
            <fieldset disabled={busy} className='space-y-7'>
              {section(
                0,
                <ChannelBasicSection>
                  <div className='grid gap-4 sm:grid-cols-2'>
                    <div className='grid content-start gap-2'>
                      <Label htmlFor='vertex-type'>类型 *</Label>
                      <Combobox
                        {...control('type')}
                        aria-label='类型'
                        options={providerOptions}
                        value='41'
                        showSelectedIcon
                        openOnFocus={false}
                        disabled={busy}
                        searchPlaceholder='搜索渠道类型…'
                        emptyText='暂无匹配的渠道类型'
                      />
                    </div>
                    <div className='grid content-start gap-2'>
                      <Label htmlFor='vertex-name'>名称 *</Label>
                      <Input
                        {...control('name')}
                        required
                        maxLength={80}
                        autoComplete='off'
                        value={name}
                        onChange={(e) => setName(e.target.value)}
                        placeholder='例如，Vertex AI 生产环境'
                      />
                      {feedback('name')}
                    </div>
                  </div>
                </ChannelBasicSection>
              )}
              {section(
                1,
                <ChannelApiAccessSection>
                  <div className={cardClass}>
                    <div className='grid justify-items-start gap-2'>
                      <Label htmlFor='vertex-format'>Vertex AI 密钥格式</Label>
                      <Picker
                        id='vertex-format'
                        value={kind}
                        items={formatItems}
                        onChange={chooseFormat}
                        disabled={busy}
                      />
                      <p className={descriptionClass}>
                        {isKey
                          ? 'API Key 模式（不支持批量创建）'
                          : 'JSON 格式支持服务账号 JSON 文件。'}
                      </p>
                    </div>
                    {isKey && (
                      <div className='grid gap-2'>
                        <Label htmlFor='vertex-project'>
                          Vertex AI 项目 ID
                        </Label>
                        <Input
                          {...control('project')}
                          value={project}
                          onChange={(e) => setProject(e.target.value)}
                          placeholder='例如 my-project-123'
                          autoComplete='off'
                        />
                        <p className={descriptionClass}>
                          可选。不填时由 Google
                          决定区域；填写后，请求会发往下方的部署区域。
                        </p>
                        {feedback('project')}
                      </div>
                    )}
                    <div className='grid gap-2'>
                      <Label htmlFor='vertex-region'>部署区域 *</Label>
                      <Textarea
                        {...control('region')}
                        required
                        rows={2}
                        maxLength={8192}
                        spellCheck={false}
                        value={region}
                        onChange={(e) => setRegion(e.target.value)}
                        placeholder={
                          '例如 ' + template.default_region + ' 或 JSON 映射'
                        }
                      />
                      <p className={descriptionClass}>
                        填写一个区域或 JSON 映射，例如：
                        <code>
                          {JSON.stringify({ default: template.default_region })}
                        </code>
                        。允许区域：{template.regions.join('、')}。
                      </p>
                      {feedback('region')}
                    </div>
                    <ChannelAuthSection>
                      <div className='flex items-center justify-between gap-4'>
                        <Label
                          htmlFor='vertex-mode'
                          className='text-muted-foreground text-xs'
                        >
                          添加模式
                        </Label>
                        <Picker
                          id='vertex-mode'
                          className='w-48 max-w-1/2'
                          value='single'
                          items={[{ value: 'single', label: '单密钥' }]}
                          disabled={busy}
                        />
                      </div>
                      <div className='grid gap-2'>
                        <Label htmlFor='vertex-credential'>
                          {formatLabel} *
                        </Label>
                        <Textarea
                          {...control('credential')}
                          required
                          value={credential}
                          onChange={(e) => {
                            sequence.current++
                            setCredential(e.target.value)
                          }}
                          maxLength={131072}
                          rows={3}
                          autoComplete='off'
                          spellCheck={false}
                          placeholder={
                            isKey
                              ? '输入来自提供商的 API 密钥'
                              : '粘贴完整的服务账号 JSON'
                          }
                        />
                        <p className={descriptionClass}>
                          {isKey
                            ? '来自提供商的 API 密钥'
                            : '可直接粘贴 JSON，也可以从文件导入。'}
                        </p>
                        {feedback('credential')}
                      </div>
                      {!isKey && (
                        <div className='grid gap-2'>
                          <Label htmlFor='vertex-file'>
                            服务账号 JSON 文件
                          </Label>
                          <Input
                            id='vertex-file'
                            type='file'
                            accept='.json,application/json'
                            onChange={loadFile}
                          />
                        </div>
                      )}
                    </ChannelAuthSection>
                  </div>
                </ChannelApiAccessSection>
              )}
              {section(
                2,
                <ChannelModelsSection>
                  <div className={cardClass}>
                    <div className='flex items-start justify-between gap-3'>
                      <div className='space-y-1'>
                        <Label htmlFor='vertex-models'>模型 *</Label>
                        <p id='vertex-models-hint' className={descriptionClass}>
                          手动填写此账号支持的模型
                          ID，每行一个，也可用逗号分隔。重复名称只提交一次。
                        </p>
                      </div>
                      <Badge variant='outline'>已填 {models.length} 个</Badge>
                    </div>
                    <Textarea
                      {...control('models')}
                      aria-describedby={
                        attempted && errors.models
                          ? 'vertex-models-hint vertex-error-models'
                          : 'vertex-models-hint'
                      }
                      required
                      rows={3}
                      value={modelText}
                      onChange={(event) => setModelText(event.target.value)}
                      autoComplete='off'
                      autoCapitalize='none'
                      spellCheck={false}
                      placeholder='请输入模型 ID'
                    />
                    {feedback('models')}
                  </div>
                  <p className='text-muted-foreground text-xs'>
                    资源分组由平台配置，无需填写。
                  </p>
                </ChannelModelsSection>
              )}
            </fieldset>
          </form>
        </div>
        <SheetFooter className={sideDrawerFooterClassName()}>
          {error && <Message>{error}</Message>}
          <Button
            type='button'
            variant='outline'
            disabled={busy}
            onClick={close}
          >
            取消
          </Button>
          <Button type='submit' form='channel-form' disabled={busy}>
            {busy ? '正在提交…' : '保存修改'}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
