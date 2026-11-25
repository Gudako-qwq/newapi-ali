import React, { useId, useState } from 'react'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'

export function Page({ title, actions, children }) {
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{title}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>{actions}</SectionPageLayout.Actions>
      <SectionPageLayout.Content>{children}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
export function Field({ label, children, hint }) {
  const id = useId()
  return (
    <div className='grid min-w-0 gap-2'>
      <Label htmlFor={id}>{label}</Label>
      {React.cloneElement(children, { id })}
      {hint && <p className='text-muted-foreground text-xs'>{hint}</p>}
    </div>
  )
}
export function SelectBox({ options, className, ...props }) {
  return (
    <NativeSelect {...props} className={className || 'w-full'}>
      {options.map(([value, label]) => (
        <option key={value} value={value}>
          {label}
        </option>
      ))}
    </NativeSelect>
  )
}
export function Message({ children }) {
  return children ? (
    <p
      role='alert'
      className='border-destructive/25 bg-destructive/5 text-destructive rounded-md border px-3 py-2 text-sm'
    >
      {children}
    </p>
  ) : null
}
export function TableView({ columns, rows, empty = '暂无数据' }) {
  return (
    <StaticDataTable
      columns={columns.map(([id, header, cell]) => ({
        id,
        header,
        cell: cell || ((row) => row[id] ?? '—'),
      }))}
      data={rows || []}
      getRowKey={(row, index) => row.id ?? index}
      emptyContent={empty}
    />
  )
}
export function FormDialog({
  title,
  description,
  close,
  save,
  submit = '保存修改',
  children,
}) {
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const id = useId()
  async function handle(event) {
    event.preventDefault()
    if (busy) return
    const fields = Object.fromEntries(new FormData(event.currentTarget))
    setBusy(true)
    setError('')
    try {
      await save(fields)
      close()
    } catch (reason) {
      setError(reason.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={(open) => !open && !busy && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form id={id} onSubmit={handle} className='grid gap-4'>
          {children}
          <Message>{error}</Message>
        </form>
        <DialogFooter>
          <Button variant='outline' disabled={busy} onClick={close}>
            取消
          </Button>
          <Button type='submit' form={id} disabled={busy}>
            {busy ? '处理中…' : submit}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
