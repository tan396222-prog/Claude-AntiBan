import React, { createContext, memo, useContext, useMemo } from "react";
import { Marked } from "marked";
import { Bell, Check, ChevronRight, CircleAlert, Terminal } from "lucide-react";
import { ProjectMedia, projectMediaKind, webURL } from "./transcript-media";
import "./transcript.css";

export const TranscriptContext = createContext(null);
const statuses = {
  completed: "已完成",
  failed: "失败",
  killed: "已终止",
  stopped: "已停止",
  running: "运行中",
  pending: "等待中",
  called: "已调用",
};
const wrapperLabels = {
  "task-notification": "后台任务",
  "system-reminder": "系统提示",
  "local-command-stdout": "命令输出",
  "local-command-stderr": "命令错误",
  "command-name": "命令",
  "command-message": "命令消息",
  "command-args": "命令参数",
};
const wrapper =
  /^ {0,3}<(task-notification|system-reminder|local-command-std(?:out|err)|command-(?:name|message|args))>([\s\S]*?)<\/\1>/;
const markdown = new Marked({
  gfm: true,
  breaks: true,
  extensions: [
    {
      name: "claudeRecord",
      level: "block",
      start(src) {
        return src.search(
          /^ {0,3}<(?:task-notification|system-reminder|local-command-std(?:out|err)|command-(?:name|message|args))>/m,
        );
      },
      tokenizer(src) {
        const m = wrapper.exec(src);
        if (m)
          return {
            type: "claudeRecord",
            raw: m[0],
            tag: m[1],
            text: m[2].trim(),
          };
      },
    },
  ],
});

function Record({ token }) {
  if (token.tag !== "task-notification")
    return (
      <details className="claude-record">
        <summary>
          <Terminal size={16} />
          {wrapperLabels[token.tag]}
          <ChevronRight size={15} />
        </summary>
        <pre>{token.text}</pre>
      </details>
    );
  const field = (name) =>
    token.text
      .match(new RegExp("<" + name + ">([\\s\\S]*?)</" + name + ">"))?.[1]
      ?.trim();
  const status = field("status");
  const Icon =
    status === "completed" ? Check : status === "failed" ? CircleAlert : Bell;
  const known = new Set(["summary", "status"]);
  const fields = [...token.text.matchAll(/<([\w-]+)>([\s\S]*?)<\/\1>/g)].filter(
    (m) => !known.has(m[1]),
  );
  const names = {
    "task-id": "任务编号",
    "tool-use-id": "工具调用",
    "output-file": "输出文件",
    result: "结果",
    usage: "用量",
  };
  return (
    <details className="claude-record task-record">
      <summary>
        <Icon className={status === "failed" ? "bad" : "task-icon"} size={17} />
        <span>
          <small>
            后台任务
            {status &&
              " · " +
                (Object.hasOwn(statuses, status) ? statuses[status] : status)}
          </small>
          <strong>{field("summary") || "任务通知"}</strong>
        </span>
        <ChevronRight size={15} />
      </summary>
      {fields.length ? (
        <dl>
          {fields.map(([, name, value], i) => (
            <React.Fragment key={i}>
              <dt>{Object.hasOwn(names, name) ? names[name] : name}</dt>
              <dd>{value.trim()}</dd>
            </React.Fragment>
          ))}
        </dl>
      ) : (
        <pre>{token.text}</pre>
      )}
    </details>
  );
}

function Reference({ token }) {
  const { sessionID, openWeb } = useContext(TranscriptContext);
  const href = token.href;
  const url = webURL(href);
  if (url)
    return (
      <a
        href={url}
        title={"用远程浏览器打开 " + new URL(url).hostname}
        onClick={(e) => {
          e.preventDefault();
          openWeb(url);
        }}
      >
        {token.type === "image" ? (
          token.text || "查看图片"
        ) : (
          <Tokens list={token.tokens} />
        )}
      </a>
    );
  if (projectMediaKind(href))
    return (
      <ProjectMedia
        key={href}
        reference={href}
        label={token.text}
        sessionID={sessionID}
      />
    );
  return <span title={href}>{token.text || href}</span>;
}

function CodeSpan({ token }) {
  const { sessionID } = useContext(TranscriptContext);
  if (
    projectMediaKind(token.text) &&
    (/^(?:[./]|[A-Za-z]:[\\/])/.test(token.text) || !/\s/.test(token.text))
  )
    return (
      <ProjectMedia
        key={token.text}
        reference={token.text}
        sessionID={sessionID}
      />
    );
  return <code>{token.text}</code>;
}

// Create React elements only. Transcript HTML (including partial tags during
// streaming) remains selectable text, with no access to the phone bridge.
function Token({ token: t }) {
  const children = t.tokens && <Tokens list={t.tokens} />;
  switch (t.type) {
    case "space":
    case "def":
      return null;
    case "claudeRecord":
      return <Record token={t} />;
    case "heading": {
      const H = "h" + Math.max(2, t.depth);
      return <H>{children}</H>;
    }
    case "paragraph":
      return <p>{children}</p>;
    case "br":
      return <br />;
    case "hr":
      return <hr />;
    case "strong":
      return <strong>{children}</strong>;
    case "em":
      return <em>{children}</em>;
    case "del":
      return <del>{children}</del>;
    case "blockquote":
      return <blockquote>{children}</blockquote>;
    case "list": {
      const List = t.ordered ? "ol" : "ul";
      return (
        <List start={t.ordered ? t.start : undefined}>
          {t.items.map((item, i) => (
            <li key={i} className={item.task ? "check-item" : undefined}>
              <Tokens list={item.tokens} />
            </li>
          ))}
        </List>
      );
    }
    case "checkbox":
      return (
        <input
          type="checkbox"
          checked={t.checked}
          disabled
          aria-label={t.checked ? "已完成" : "未完成"}
        />
      );
    case "codespan":
      return <CodeSpan token={t} />;
    case "code":
      return (
        <div className="code-block">
          {t.lang && <small>{t.lang.split(/\s+/)[0]}</small>}
          <pre tabIndex={0}>
            <code>{t.text}</code>
          </pre>
        </div>
      );
    case "image":
    case "link":
      return <Reference token={t} />;
    case "table":
      return (
        <div className="md-table" tabIndex={0}>
          <table>
            <thead>
              <tr>
                {t.header.map((c, i) => (
                  <th key={i} style={{ textAlign: c.align }}>
                    <Tokens list={c.tokens} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {t.rows.map((row, r) => (
                <tr key={r}>
                  {row.map((c, i) => (
                    <td key={i} style={{ textAlign: c.align }}>
                      <Tokens list={c.tokens} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    case "html":
      return <span className="literal-markup">{t.raw}</span>;
    default:
      return children || t.text || t.raw;
  }
}
function Tokens({ list }) {
  return list?.map((t, i) => <Token key={i} token={t} />);
}
export const MessageBody = memo(function MessageBody({ text }) {
  const tokens = useMemo(() => {
    try {
      return markdown.lexer(text);
    } catch {
      return [{ type: "html", raw: text }];
    }
  }, [text]);
  return (
    <div className="message-body markdown">
      <Tokens list={tokens} />
    </div>
  );
});

export function ToolActivity({ tools }) {
  if (!tools?.length) return null;
  const failures = tools.filter((t) => t.status === "failed").length;
  return (
    <details className="claude-record tool-history">
      <summary>
        <Terminal size={16} />
        <span>
          {tools.length} 次工具调用
          {failures > 0 && <small className="bad"> · {failures} 次失败</small>}
        </span>
        <ChevronRight size={15} />
      </summary>
      <ul>
        {tools.map((tool) => (
          <li key={tool.id}>
            <strong>{tool.name}</strong>
            <small className={tool.status === "failed" ? "bad" : ""}>
              {statuses[tool.status] || "已调用"}
            </small>
            {tool.detail && <p>{tool.detail}</p>}
          </li>
        ))}
      </ul>
    </details>
  );
}
