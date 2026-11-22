; Runs inside Tauri's uninstaller. Updates pass through it too ($UpdateMode), so
; assistants are only restored on a real, interactive uninstall the user agreed to.
!macro NSIS_HOOK_PREUNINSTALL
  ${If} $UpdateMode <> 1
  ${AndIf} $PassiveMode <> 1
    IfSilent jieshangai_restore_done
    MessageBox MB_YESNO|MB_ICONQUESTION "要同时恢复 Claude 和 Codex 的设置吗？$\r$\n$\r$\n选“是”：去掉接上AI写入的连接、PATH、MCP 服务和 Skills。$\r$\n选“否”：助手继续按现在的设置使用接上AI。" /SD IDNO IDNO jieshangai_restore_done
    Push $0
    nsExec::Exec '"$INSTDIR\jieshangai-engine.exe" --restore-on-uninstall'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_OK|MB_ICONEXCLAMATION "没能完整恢复设置。可以重新安装接上AI后，在设置里点“全部恢复”，或联系客服。" /SD IDOK
    ${EndIf}
    Pop $0
    jieshangai_restore_done:
  ${EndIf}
!macroend
