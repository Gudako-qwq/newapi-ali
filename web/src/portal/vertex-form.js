export function defaultVertexTemplate(templates) {
  return (
    templates.find((item) => item.credential_kind === 'vertex_json') ??
    templates[0]
  )
}

export function parseModelNames(text) {
  return [
    ...new Set(
      text
        .split(/[,，\r\n]+/u)
        .map((name) => name.trim())
        .filter(Boolean)
    ),
  ]
}

export function modelNameError(models) {
  if (!models.length) return '请至少填写一个模型 ID。'
  if (models.length > 100) return '每个渠道最多填写 100 个模型 ID。'
  for (const model of models) {
    if (
      !/^[\p{L}\p{N}]/u.test(model) ||
      /[^\p{L}\p{N}._+@:/-]/u.test(model) ||
      [...model].length > 256 ||
      model.split('/').some((part) => !part || part === '.' || part === '..')
    ) {
      return '模型 ID 须以字母或数字开头，最多 256 字符；请填写模型名称，不要填写网址或空格。'
    }
  }
  return ''
}

export function regionError(raw, template, models) {
  const value = raw.trim()
  if (!value) return '请填写部署区域。'
  if (!value.startsWith('{'))
    return template.regions.includes(value) ? '' : '请选择模板允许的部署区域。'
  if (new TextEncoder().encode(value).length > 8192)
    return '部署区域映射不能超过 8 KB。'
  let mapping
  try {
    mapping = JSON.parse(value)
  } catch {
    return '部署区域映射不是有效的 JSON。'
  }
  if (!Object.hasOwn(mapping, 'default') || !mapping.default)
    return '部署区域映射须包含 default。'
  for (const [model, region] of Object.entries(mapping)) {
    if (!template.regions.includes(region))
      return '部署区域映射包含未允许的区域。'
    if (model !== 'default' && !models.includes(model))
      return '部署区域映射只能包含已填写的模型。'
  }
  return ''
}

export function validateVertex({
  template,
  name,
  credential,
  project,
  region,
  models,
}) {
  const errors = {}
  if (!template || template.type !== 41) errors.type = '请选择 Vertex AI。'
  if (!name.trim() || [...name.trim()].length > 80)
    errors.name = '请填写渠道名称（最多 80 字）。'
  const key = credential.trim()
  if (!key)
    errors.credential =
      template?.credential_kind === 'api_key'
        ? '请填写 API 密钥。'
        : '请粘贴或导入服务账号 JSON。'
  else if (new TextEncoder().encode(credential).length > 131072)
    errors.credential = '凭据不得超过 128 KB。'
  else if (template?.credential_kind === 'api_key') {
    if (key.length < 8 || /[\r\n]/.test(key))
      errors.credential = '一次只能填写一个有效 API 密钥（至少 8 个字符）。'
  } else {
    try {
      const account = JSON.parse(key)
      if (
        account?.type !== 'service_account' ||
        !account.project_id ||
        typeof account.client_email !== 'string' ||
        !account.client_email.endsWith('.iam.gserviceaccount.com') ||
        typeof account.private_key !== 'string' ||
        !account.private_key.startsWith('-----BEGIN PRIVATE KEY-----') ||
        account.token_uri !== 'https://oauth2.googleapis.com/token'
      )
        throw Error()
    } catch {
      errors.credential = '请提供完整的 Google 服务账号 JSON。'
    }
  }
  if (
    template?.credential_kind === 'api_key' &&
    project &&
    !/^[a-z][a-z0-9-]{4,61}[a-z0-9]$/.test(project.trim())
  )
    errors.project = '请检查 Vertex AI 项目 ID。'
  if (template) {
    const message = regionError(region, template, models)
    if (message) errors.region = message
  }
  const modelError = modelNameError(models)
  if (modelError) errors.models = modelError
  return errors
}
