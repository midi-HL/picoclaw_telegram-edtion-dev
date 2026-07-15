import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { launcherFetch } from "@/api/http"

interface StickerItem {
  id: string
  source_type: "manual" | "telegram_set"
  sticker_set_name?: string
  file_path: string
  telegram_file_id?: string
  emoji_hint?: string
  description: string
  usage_scenarios: string
  created_at: string
}

async function safeFetchJson<T>(
  url: string,
  options?: RequestInit,
): Promise<{ data?: T; error?: string }> {
  try {
    const res = await launcherFetch(url, options)
    const contentType = res.headers.get("content-type")
    if (!contentType || !contentType.includes("application/json")) {
      const text = await res.text()
      return {
        error: `服务器返回了非 JSON 格式响应 (状态码: ${res.status}): ${text.substring(0, 100)}`,
      }
    }
    const data = await res.json()
    if (!res.ok) {
      return { error: (data as any).error || `请求失败 (状态码: ${res.status})`, data: data as T }
    }
    return { data: data as T }
  } catch (err: any) {
    return { error: err?.message || "网络请求发生未知错误" }
  }
}

export function TelegramStickersManager() {
  const { t } = useTranslation()
  const [stickers, setStickers] = useState<StickerItem[]>([])
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [mode, setMode] = useState<"manual" | "import">("manual")
  const [loading, setLoading] = useState(false)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)

  // Mode A form
  const [manualFile, setManualFile] = useState<File | null>(null)
  const [stickerId, setStickerId] = useState("")
  const [emojiHint, setEmojiHint] = useState("")
  const [description, setDescription] = useState("")
  const [scenarios, setScenarios] = useState("")

  // Mode B form
  const [setLink, setSetLink] = useState("")

  const fileInputRef = useRef<HTMLInputElement>(null)

  const fetchStickers = useCallback(async () => {
    setErrorMessage(null)
    const { data, error } = await safeFetchJson<{ stickers: StickerItem[] }>(
      "/api/telegram/stickers",
    )
    if (error) {
      setErrorMessage(error)
    } else if (data) {
      setStickers(data.stickers || [])
      setSelectedIds([])
    }
  }, [])

  useEffect(() => {
    fetchStickers()
  }, [fetchStickers])

  const handleManualSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!manualFile || !stickerId || !description || !scenarios) {
      setErrorMessage("请填写所有手动录入的必填项！")
      return
    }
    setLoading(true)
    setErrorMessage(null)

    const formData = new FormData()
    formData.append("file", manualFile)
    formData.append("id", stickerId)
    formData.append("emoji_hint", emojiHint)
    formData.append("description", description)
    formData.append("usage_scenarios", scenarios)

    try {
      const res = await launcherFetch("/api/telegram/stickers/manual", {
        method: "POST",
        body: formData,
      })
      const contentType = res.headers.get("content-type")
      if (contentType && contentType.includes("application/json")) {
        const data = await res.json()
        if (!res.ok) {
          setErrorMessage((data as any).error || "手动上传失败")
        } else {
          fetchStickers()
          setManualFile(null)
          setStickerId("")
          setEmojiHint("")
          setDescription("")
          setScenarios("")
          if (fileInputRef.current) fileInputRef.current.value = ""
        }
      } else {
        const text = await res.text()
        setErrorMessage(`非JSON格式响应: ${text.substring(0, 100)}`)
      }
    } catch (err: any) {
      setErrorMessage(err.message || "请求失败")
    } finally {
      setLoading(false)
    }
  }

  const handleImportSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!setLink) return
    setLoading(true)
    setErrorMessage(null)

    let packName = setLink.trim()
    if (packName.includes("addstickers/")) {
      packName = packName.split("addstickers/")[1]
    }

    const { data, error } = await safeFetchJson<any>(
      "/api/telegram/stickers/import-set",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sticker_set_name: packName }),
      },
    )

    if (error) {
      setErrorMessage(error)
    } else {
      fetchStickers()
      setSetLink("")
    }
    setLoading(false)
  }

  const handleDelete = async (id: string) => {
    if (!confirm(`确定要删除表情包 "${id}" 吗？`)) return
    setLoading(true)
    const { error } = await safeFetchJson<any>(
      `/api/telegram/stickers/${id}`,
      { method: "DELETE" },
    )
    if (error) {
      setErrorMessage(error)
    } else {
      fetchStickers()
    }
    setLoading(false)
  }

  const handleBatchDelete = async () => {
    if (selectedIds.length === 0) return
    if (!confirm(`确定要批量删除已选择的 ${selectedIds.length} 个表情包吗？`))
      return
    setLoading(true)
    setErrorMessage(null)

    const { error } = await safeFetchJson<any>(
      "/api/telegram/stickers/batch-delete",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ids: selectedIds }),
      },
    )

    if (error) {
      setErrorMessage(error)
    } else {
      fetchStickers()
    }
    setLoading(false)
  }

  const toggleSelect = (id: string) => {
    setSelectedIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id],
    )
  }

  const toggleSelectAll = () => {
    if (selectedIds.length === stickers.length) {
      setSelectedIds([])
    } else {
      setSelectedIds(stickers.map((s) => s.id))
    }
  }

  return (
    <div className="space-y-6">
      {/* Guide */}
      <Card className="shadow-sm">
        <CardContent className="px-6 py-4 text-sm space-y-2 text-muted-foreground">
          <p className="font-medium text-foreground">
            Telegram 自定义表情包 (Stickers) 管理
          </p>
          <p>
            1. <b>共享机制</b>：所有表情数据存储在共享文件{" "}
            <code className="text-xs bg-muted px-1 rounded">
              ~/.picoclaw/telegram_stickers.json
            </code>{" "}
            中，Launcher 与 Gateway 进程共享。
          </p>
          <p>
            2. <b>自动降级</b>：Bot 在接收 TGS 或 WebM 动画贴纸时，将自动降级下载其静态缩略图传入大模型多模态理解。
          </p>
          <p>
            3. <b>容错策略</b>：即使没有生成详细画面描述，模型仍能参考 Emoji 关联提示与适用场景进行精准交互。
          </p>
        </CardContent>
      </Card>

      {/* Error display */}
      {errorMessage && (
        <div className="bg-destructive/10 text-destructive rounded-lg px-4 py-3 text-sm">
          {errorMessage}
        </div>
      )}

      {/* Add form */}
      <Card className="shadow-sm">
        <CardContent className="px-6 py-4 space-y-4">
          <Label className="text-base font-bold">录入新表情包</Label>
          <div className="flex gap-4">
            <button
              type="button"
              onClick={() => setMode("manual")}
              className={`text-sm font-medium pb-1 border-b-2 transition-colors ${
                mode === "manual"
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              模式 A: 本地手动上传
            </button>
            <button
              type="button"
              onClick={() => setMode("import")}
              className={`text-sm font-medium pb-1 border-b-2 transition-colors ${
                mode === "import"
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              模式 B: 贴纸链接导入 (LLM 自动生成描述)
            </button>
          </div>

          {mode === "manual" ? (
            <form onSubmit={handleManualSubmit} className="space-y-4 pt-2">
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>StickerID (英文、数字、下划线)</Label>
                  <Input
                    value={stickerId}
                    onChange={(e) => setStickerId(e.target.value)}
                    placeholder="如: funny_cat"
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label>关联快捷 Emoji (选填)</Label>
                  <Input
                    value={emojiHint}
                    onChange={(e) => setEmojiHint(e.target.value)}
                    placeholder="如: 😂"
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label>上传表情图片 (WEBP/PNG/JPG)</Label>
                <Input
                  ref={fileInputRef}
                  type="file"
                  onChange={(e) =>
                    setManualFile(e.target.files?.[0] || null)
                  }
                  accept="image/*"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label>画面内容详细描述</Label>
                <Textarea
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="描述例如：一只猫咪双手合十闭着眼睛露出微笑..."
                  required
                />
              </div>
              <div className="space-y-2">
                <Label>最适合使用的聊天场景</Label>
                <Textarea
                  value={scenarios}
                  onChange={(e) => setScenarios(e.target.value)}
                  placeholder="场景例如：用户表达感谢、夸赞、或氛围温馨时..."
                  required
                />
              </div>
              <Button type="submit" disabled={loading}>
                {loading ? "处理中..." : "录入表情"}
              </Button>
            </form>
          ) : (
            <form onSubmit={handleImportSubmit} className="space-y-4 pt-2">
              <div className="space-y-2">
                <Label>
                  Telegram 贴纸包链接或名称 (如 LovelyPanda)
                </Label>
                <Input
                  value={setLink}
                  onChange={(e) => setSetLink(e.target.value)}
                  placeholder="https://t.me/addstickers/LovelyPanda 或 LovelyPanda"
                  required
                />
              </div>
              <Button type="submit" disabled={loading} variant="secondary">
                {loading ? "导入中..." : "一键自动导入套图"}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>

      {/* Sticker list */}
      <div className="space-y-4">
        <div className="flex justify-between items-center border-b pb-2">
          <div className="flex items-center space-x-2">
            <Button
              variant="outline"
              size="sm"
              onClick={toggleSelectAll}
              disabled={stickers.length === 0}
            >
              {selectedIds.length === stickers.length && stickers.length > 0
                ? "取消全选"
                : "全选"}
            </Button>
            <span className="text-sm text-muted-foreground">
              已选中 {selectedIds.length} / {stickers.length}
            </span>
          </div>
          {selectedIds.length > 0 && (
            <Button
              variant="destructive"
              size="sm"
              onClick={handleBatchDelete}
              disabled={loading}
            >
              批量删除 ({selectedIds.length})
            </Button>
          )}
        </div>

        {stickers.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">
            暂无表情包，使用上方表单录入或导入。
          </p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {stickers.map((item) => {
              const isSelected = selectedIds.includes(item.id)
              return (
                <Card
                  key={item.id}
                  className={`overflow-hidden border relative transition-all ${
                    isSelected
                      ? "border-primary ring-1 ring-primary"
                      : "border-border/60"
                  }`}
                >
                  <div className="absolute top-2 left-2 z-10">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleSelect(item.id)}
                      className="w-4 h-4 rounded border-slate-300 cursor-pointer"
                    />
                  </div>
                  <div className="h-40 bg-muted/30 flex items-center justify-center p-2 pt-8">
                    <img
                      src={`/api/telegram/stickers/${item.id}/image`}
                      alt={item.id}
                      className="max-h-full max-w-full object-contain"
                      onError={(e) => {
                        // Fallback: try loading directly from file_path
                        const target = e.target as HTMLImageElement
                        if (target.src !== item.file_path) {
                          target.src = item.file_path
                        }
                      }}
                    />
                  </div>
                  <CardContent className="p-3 space-y-2 text-xs">
                    <div className="flex justify-between items-center">
                      <span className="font-bold text-sm text-primary">
                        {item.id}
                      </span>
                      <span className="bg-muted text-muted-foreground px-2 py-0.5 rounded text-[10px]">
                        {item.source_type === "manual"
                          ? "手动上传"
                          : `${item.sticker_set_name} 导入`}
                      </span>
                    </div>
                    <div>
                      <b>关联表情:</b> {item.emoji_hint || "无"}
                    </div>
                    <div className="text-muted-foreground line-clamp-3">
                      <b>画面描述:</b>{" "}
                      {item.description || (
                        <span className="italic">无（直接关联 Emoji 启动）</span>
                      )}
                    </div>
                    <div className="text-muted-foreground line-clamp-2">
                      <b>适用语境:</b> {item.usage_scenarios}
                    </div>
                    <div className="pt-2 flex justify-end">
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => handleDelete(item.id)}
                        disabled={loading}
                      >
                        删除
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
