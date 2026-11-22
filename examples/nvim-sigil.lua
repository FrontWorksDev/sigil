-- LazyVim をコンテナでも使うための追記（ホストの Neovim には影響しません）
-- sigil の中では環境変数 SIGIL=1 が入っているので、それで分岐します。

----------------------------------------------------------------------
-- 1) lua/config/lazy.lua
--    require("lazy").setup({ ... }) の中に lockfile を追加する。
--    コンテナからは設定フォルダが読み取り専用なので、lazy-lock.json を
--    データ側に書かせる。ホストでは今までどおり設定フォルダに書く。
----------------------------------------------------------------------
--[[
require("lazy").setup({
  spec = {
    -- ...（今の設定のまま）
  },
  lockfile = vim.env.SIGIL
      and (vim.fn.stdpath("data") .. "/lazy-lock.json")
    or (vim.fn.stdpath("config") .. "/lazy-lock.json"),
  -- ...（今の設定のまま）
})
]]

----------------------------------------------------------------------
-- 2) lua/config/options.lua の末尾に追加
--    コンテナから Mac のクリップボードへコピーできるように、OSC 52 を使う。
--    （ペーストは端末の設定によっては確認が出るか、使えないことがあります。
--      その場合は Cmd+V で端末から貼り付けてください）
----------------------------------------------------------------------
if vim.env.SIGIL then
  local osc52 = require("vim.ui.clipboard.osc52")
  vim.g.clipboard = {
    name = "OSC 52",
    copy = { ["+"] = osc52.copy("+"), ["*"] = osc52.copy("*") },
    paste = { ["+"] = osc52.paste("+"), ["*"] = osc52.paste("*") },
  }
  vim.opt.clipboard = "unnamedplus"
end
