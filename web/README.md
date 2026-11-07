# @lariat-labs/oversight-element

The Oversight UI as a single web component. It needs a running [Oversight API](https://github.com/Lariat-Labs/oversightLLM).

```bash
npm install @lariat-labs/oversight-element --registry=https://npm.pkg.github.com
```

```js
import "@lariat-labs/oversight-element";
```

```html
<oversight-app api-base="http://127.0.0.1:8080" theme="auto" routing="hash"></oversight-app>
```

Attributes: `api-base` (default: the page's origin), `theme` (`light` | `dark` | `auto`), `agent` (initial agent),
`routing="hash"` (mirror the view in the URL). Emits `oversight:navigate` with the current route.
Allow the host page's origin in the API's `OVERSIGHT_CORS_ORIGINS`.
