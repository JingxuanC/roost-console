import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Session } from '@supabase/supabase-js';
import { supabase } from './lib/supabase';
import { api, type BillingView, type MeResponse, type Order, type Plan } from './lib/api';

const PRODUCT_LABELS: Record<string, string> = {
  dslpha: 'DeepSeek Alpha · 投研工作台',
  market: 'Roost Market · 算力市场',
  'causal-memory': 'Causal Memory · 智能体记忆',
};

const CREDIT_PACKS = [
  { sku: 'credit_50', label: '5,000 credits', price: '¥50' },
  { sku: 'credit_100', label: '10,000 credits', price: '¥100' },
  { sku: 'credit_500', label: '50,000 credits', price: '¥500' },
];

const WORKSPACES = [
  {
    key: 'sandbox',
    meta: 'WORKSPACE',
    title: 'Roost Sandbox',
    desc: '创建云电脑、打开 Terminal、管理快照和规格。',
    action: '打开 Sandbox',
    href: '/market/sandboxes?sso=1&next=%2Fsandboxes',
  },
  {
    key: 'market',
    meta: 'MARKET',
    title: 'Roost Market',
    desc: 'Provider 节点、租约、上架容量与收益。',
    action: '进入 Market',
    href: '/market/?sso=1&next=%2Fmarket',
  },
  {
    key: 'billing',
    meta: 'ORGANIZATION',
    title: '组织与账单',
    desc: '余额、套餐、订单、用量与发票。',
    action: '查看账单',
    href: '#billing',
  },
];

function Workspaces() {
  return (
    <section className="workspaces" aria-labelledby="workspaces-title">
      <div className="workspaces__head">
        <div>
          <p className="kicker">START HERE</p>
          <h2 id="workspaces-title">一个组织，四个工作台。</h2>
        </div>
        <p className="workspaces__intro">
          所有工作台共享同一个组织、同一份余额和同一套凭证。从这里进入，不用再记多个账号。
        </p>
      </div>
      <div className="workspace-grid">
        {WORKSPACES.map((item) => (
          <a className="workspace-card" href={item.href} key={item.key}>
            <div className="workspace-card__top">
              <span className="workspace-card__meta">{item.meta}</span>
              <span className="workspace-card__arrow" aria-hidden="true">→</span>
            </div>
            <h3>{item.title}</h3>
            <p>{item.desc}</p>
            <span className="workspace-card__action">{item.action}</span>
          </a>
        ))}
      </div>
    </section>
  );
}

function credits(n: number) {
  return new Intl.NumberFormat('zh-CN').format(n);
}

function yuan(cents: number) {
  return `¥${(cents / 100).toFixed(2)}`;
}

function planLabel(code: string) {
  return code === 'byok-pro' ? 'BYOK Pro' : code.charAt(0).toUpperCase() + code.slice(1);
}

function Logo() {
  return (
    <a className="brand" href="/" aria-label="Roost Console">
      <span className="brand__mark" aria-hidden="true">
        <span />
        <span />
      </span>
      <span className="brand__name">roost</span>
      <span className="brand__tag">console</span>
    </a>
  );
}

function AuthScreen() {
  const [mode, setMode] = useState<'login' | 'signup'>('login');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    setNotice('');
    try {
      if (mode === 'signup') {
        const { data, error } = await supabase.auth.signUp({
          email,
          password,
          options: { emailRedirectTo: `${window.location.origin}/console/` },
        });
        if (error) throw error;
        if (!data.session) {
          setNotice('账号已创建，请查收验证邮件后登录。');
        }
      } else {
        const { error } = await supabase.auth.signInWithPassword({ email, password });
        if (error) throw error;
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth">
      <section className="auth__story">
        <Logo />
        <div className="auth__story-body">
          <p className="eyebrow"><span className="dot" /> ONE ORGANIZATION</p>
          <h1>一个账号，<br /><span>所有工作台。</span></h1>
          <p className="auth__lead">
            Roost 组织统一账号、积分和账单。注册之后，Sandbox、Market、dsalpha、causal-memory
            共用同一份余额，不用再分别充值。
          </p>
          <div className="auth__ledger">
            <div className="auth__ledger-head">
              <span>org_roost</span>
              <span className="tag tag--live">active</span>
            </div>
            <div className="auth__ledger-row"><span>余额</span><strong>12,480 credits</strong></div>
            <div className="auth__ledger-row"><span>产品</span><span>sandbox · market · dslpha · causal-memory</span></div>
            <div className="auth__ledger-row"><span>账单</span><span>一个组织，一份账单</span></div>
          </div>
        </div>
        <p className="auth__foot">Roost Cloud · 统一控制面</p>
      </section>
      <section className="auth__pane">
        <div className="auth__form-wrap">
          <p className="kicker">{mode === 'login' ? 'WELCOME BACK' : 'CREATE YOUR ORG'}</p>
          <h2>{mode === 'login' ? '登录 Roost' : '创建 Roost 账号'}</h2>
          <p className="muted">
            {mode === 'login' ? '登录后进入你的组织控制台。' : '注册后自动创建一个 personal organization。'}
          </p>
          <form onSubmit={submit} className="form">
            <label>
              邮箱
              <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" required autoComplete="email" />
            </label>
            <label>
              密码
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="至少 8 位" required minLength={8} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} />
            </label>
            {error && <p className="form-error">{error}</p>}
            {notice && <p className="form-notice">{notice}</p>}
            <button className="btn btn--ink btn--wide" disabled={busy}>
              {busy ? '请稍候…' : mode === 'login' ? '登录' : '注册'}
            </button>
          </form>
          <button className="auth__switch" type="button" onClick={() => { setMode(mode === 'login' ? 'signup' : 'login'); setError(''); setNotice(''); }}>
            {mode === 'login' ? '还没有账号？创建组织' : '已有账号？直接登录'}
          </button>
        </div>
      </section>
    </main>
  );
}

function Header({ email, onSignOut }: { email: string; onSignOut: () => void }) {
  return (
    <header className="topbar">
      <Logo />
      <nav className="topbar__nav">
        <a href="/market/sandboxes?sso=1&next=%2Fsandboxes">Sandbox</a>
        <a href="/market/?sso=1&next=%2Fmarket">Market</a>
        <a className="active" href="#billing">账单</a>
        <a href="#products">产品</a>
        <a href="#plans">套餐</a>
        <a href="#orders">订单</a>
      </nav>
      <div className="topbar__right">
        <span className="who">{email}</span>
        <button className="btn btn--ghost btn--small" onClick={onSignOut}>退出</button>
      </div>
    </header>
  );
}

function WalletCard({ billing }: { billing: BillingView }) {
  return (
    <section className="wallet" id="billing">
      <div className="wallet__main">
        <p className="eyebrow"><span className="dot" /> AVAILABLE CREDITS</p>
        <div className="wallet__amount">
          <strong>{credits(billing.wallet.available_credits)}</strong>
          <span>credits</span>
        </div>
        <p className="wallet__cash">≈ ¥{(billing.wallet.available_credits / 100).toFixed(2)} · 100 credits = ¥1</p>
        <div className="wallet__meta">
          <span>总额 {credits(billing.wallet.balance_credits)}</span>
          <span>冻结 {credits(billing.wallet.reserved_credits)}</span>
          <span>套餐 {planLabel(billing.plan.code)}</span>
        </div>
      </div>
      <div className="wallet__side">
        <p className="mono">ORG_ID</p>
        <code>{billing.org.id}</code>
        <div className="wallet__side-row"><span>组织</span><strong>{billing.org.name}</strong></div>
        <div className="wallet__side-row"><span>角色</span><strong>{billing.org.role || 'owner'}</strong></div>
      </div>
    </section>
  );
}

function Products({ billing }: { billing: BillingView }) {
  return (
    <section className="section" id="products">
      <div className="section__head">
        <div>
          <p className="kicker">PRODUCTS</p>
          <h2>一个余额，所有产品。</h2>
        </div>
        <p className="section__intro">每个产品共享组织钱包，同时有独立的月度上限，避免一个任务吃光所有积分。</p>
      </div>
      <div className="product-grid">
        {billing.products.map((product) => {
          const cap = product.monthly_cap_credits ?? 0;
          const pct = cap > 0 ? Math.min(100, Math.round((product.used_this_month / cap) * 100)) : 0;
          const href =
            product.product === 'market' ? '/market/?sso=1&next=%2Fmarket' :
            product.product === 'causal-memory' ? 'https://causal-memory.com' :
            product.product === 'dslpha' ? 'https://dsalpha.roostbots.com' :
            undefined;
          const body = (
            <>
              <div className="product-card__head">
                <span className={`product-dot product-dot--${product.product}`} />
                <span className="tag">{product.enabled ? 'enabled' : 'disabled'}</span>
              </div>
              <h3>{PRODUCT_LABELS[product.product] ?? product.product}</h3>
              <p className="muted">共享余额，按实际用量扣费。</p>
              <div className="bar"><span style={{ width: `${pct}%` }} /></div>
              <div className="product-card__foot">
                <span>{credits(product.used_this_month)} / {cap ? credits(cap) : '不限'}</span>
                <span>{pct}%</span>
              </div>
            </>
          );
          return href ? (
            <a className="product-card product-card--link" href={href} key={product.product}>{body}</a>
          ) : (
            <article className="product-card" key={product.product}>{body}</article>
          );
        })}
      </div>
    </section>
  );
}

function CausalMemory({ token, orgId }: { token: string; orgId: string }) {
  const [mcpToken, setMcpToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);

  async function issue() {
    setBusy(true);
    setError('');
    try {
      const issued = await api.issueCausalToken(token, orgId);
      setMcpToken(issued.token);
    } catch (err) {
      setError(err instanceof Error ? err.message : '生成失败');
    } finally {
      setBusy(false);
    }
  }

  async function copy() {
    if (!mcpToken) return;
    await navigator.clipboard.writeText(mcpToken);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  return (
    <section className="causal" id="causal-memory">
      <div>
        <p className="kicker">CAUSAL MEMORY · MCP</p>
        <h2>把记忆接到 Agent。</h2>
        <p className="muted">
          用组织 token 连接 Causal Memory MCP。调用和存储按组织额度计费，
          与 market、dslpha 共用同一个钱包。
        </p>
        <code className="causal__endpoint">https://causal-memory.com/memory/mcp</code>
      </div>
      <div className="causal__action">
        {mcpToken ? (
          <>
            <p className="kicker">TOKEN · 只显示一次</p>
            <code className="causal__token">{mcpToken}</code>
            <button className="btn btn--ghost btn--small" onClick={() => void copy()}>{copied ? '已复制' : '复制 token'}</button>
            <p className="muted causal__hint">在 MCP 客户端里配置 Authorization: Bearer &lt;token&gt;</p>
          </>
        ) : (
          <button className="btn btn--ink" onClick={() => void issue()} disabled={busy}>{busy ? '生成中…' : '生成 MCP Token'}</button>
        )}
        {error && <p className="form-error">{error}</p>}
      </div>
    </section>
  );
}

function Plans({ plans, current, onBuy, busy }: { plans: Plan[]; current: string; onBuy: (sku: string) => void; busy: string }) {
  return (
    <section className="section" id="plans">
      <div className="section__head">
        <div>
          <p className="kicker">PLANS</p>
          <h2>按月给组织充值。</h2>
        </div>
        <p className="section__intro">虎皮椒单次支付，30 天有效；到期前提醒，不自动扣款。</p>
      </div>
      <div className="plan-grid">
        {plans.filter((plan) => plan.code !== 'free').map((plan) => {
          const featured = plan.code === 'pro';
          return (
            <article className={`plan-card ${featured ? 'plan-card--featured' : ''}`} key={plan.code}>
              {featured && <span className="plan-card__badge">推荐</span>}
              <div className="plan-card__head">
                <h3>{planLabel(plan.code)}</h3>
                <span className="tag">{plan.duration_days} 天</span>
              </div>
              <div className="plan-card__price">
                <strong>{yuan(plan.price_cents)}</strong>
                <span>/ 30 天</span>
              </div>
              <p className="muted">{credits(plan.included_credits)} credits · 全产品可用</p>
              <ul className="plan-list">
                {Object.entries(plan.product_caps).map(([product, cap]) => (
                  <li key={product}><span>{PRODUCT_LABELS[product]?.split(' · ')[0] ?? product}</span><span>{credits(cap)}</span></li>
                ))}
              </ul>
              <button className={`btn ${featured ? 'btn--accent' : 'btn--ink'} btn--wide`} disabled={busy === `plan_${plan.code}_30d` || current === plan.code} onClick={() => onBuy(`plan_${plan.code}_30d`)}>
                {current === plan.code ? '当前套餐' : busy === `plan_${plan.code}_30d` ? '创建订单…' : '购买'}
              </button>
            </article>
          );
        })}
      </div>
      <div className="packs">
        <p className="kicker">CREDIT PACKS</p>
        {CREDIT_PACKS.map((pack) => (
          <button key={pack.sku} className="pack" disabled={busy === pack.sku} onClick={() => onBuy(pack.sku)}>
            <span>{pack.label}</span>
            <strong>{pack.price}</strong>
          </button>
        ))}
      </div>
    </section>
  );
}

function OrderModal({ token, order, onClose, onPaid }: { token: string; order: Order; onClose: () => void; onPaid: () => void }) {
  const [current, setCurrent] = useState(order);
  const [error, setError] = useState('');
  const [paying, setPaying] = useState(false);
  const isDev = Boolean(current.pay_url?.includes('/dev-pay'));

  useEffect(() => {
    if (current.status === 'fulfilled' || current.status === 'paid') return;
    const id = window.setInterval(async () => {
      try {
        const { order: fresh } = await api.getOrder(token, current.id);
        setCurrent(fresh);
        if (fresh.status === 'fulfilled' || fresh.status === 'paid') {
          window.clearInterval(id);
          onPaid();
        }
      } catch { /* keep polling */ }
    }, 2000);
    return () => window.clearInterval(id);
  }, [current.id, current.status, onPaid, token]);

  async function devPay() {
    setPaying(true);
    setError('');
    try {
      await api.devPay(token, current.id);
      const { order: fresh } = await api.getOrder(token, current.id);
      setCurrent(fresh);
      onPaid();
    } catch (err) {
      setError(err instanceof Error ? err.message : '支付失败');
    } finally {
      setPaying(false);
    }
  }

  const done = current.status === 'fulfilled' || current.status === 'paid';
  return (
    <div className="modal" role="dialog" aria-modal="true" aria-label="支付订单">
      <div className="modal__panel">
        <button className="modal__close" onClick={onClose} aria-label="关闭">×</button>
        <p className="kicker">PAYMENT</p>
        <h2>{done ? '已到账' : '完成支付'}</h2>
        <p className="muted">订单 {current.id.slice(0, 8)} · {yuan(current.amount_cents)}</p>
        <div className="order-summary">
          <div><span>SKU</span><strong>{current.sku}</strong></div>
          <div><span>到账</span><strong>{credits(current.credits)} credits</strong></div>
          <div><span>状态</span><strong>{current.status}</strong></div>
        </div>
        {done ? (
          <div className="pay-done">
            <span className="pay-done__mark">✓</span>
            <p>积分已进入组织钱包，所有产品立即可用。</p>
            <button className="btn btn--ink btn--wide" onClick={onClose}>返回控制台</button>
          </div>
        ) : (
          <>
            {current.qr_url && <img className="qr" src={current.qr_url} alt="支付二维码" />}
            {!current.qr_url && isDev && (
              <p className="dev-note">本地开发模式：虎皮椒未配置，使用测试支付完成回调链路。</p>
            )}
            {error && <p className="form-error">{error}</p>}
            <div className="modal__actions">
              {isDev ? (
                <button className="btn btn--accent btn--wide" onClick={devPay} disabled={paying}>{paying ? '处理中…' : '模拟支付成功'}</button>
              ) : current.pay_url ? (
                <a className="btn btn--accent btn--wide" href={current.pay_url} target="_blank" rel="noreferrer">打开支付页面</a>
              ) : (
                <p className="muted">等待支付服务返回二维码…</p>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function Orders({ orders }: { orders: Order[] }) {
  orders = orders ?? [];
  return (
    <section className="section" id="orders">
      <div className="section__head">
        <div><p className="kicker">ORDERS</p><h2>订单记录。</h2></div>
      </div>
      <div className="table">
        <div className="table__row table__row--head"><span>时间</span><span>SKU</span><span>金额</span><span>积分</span><span>状态</span></div>
        {orders.length === 0 && <div className="table__empty">还没有订单。</div>}
        {orders.map((order) => (
          <div className="table__row" key={order.id}>
            <span>{new Date(order.created_at).toLocaleString('zh-CN')}</span>
            <span className="mono">{order.sku}</span>
            <span>{yuan(order.amount_cents)}</span>
            <span>{credits(order.credits)}</span>
            <span><span className={`status status--${order.status}`}>{order.status}</span></span>
          </div>
        ))}
      </div>
    </section>
  );
}

// First-party clients are trusted for the identity handoff: the user has
// already signed into the unified Roost organization, and the server still
// validates the registered redirect_uri + client secret at token exchange.
// Any other client keeps the explicit consent screen.
const FIRST_PARTY_OIDC_CLIENTS = new Set(['market', 'roostbot']);

function OAuthAuthorize({ session }: { session: Session }) {
  const params = new URLSearchParams(window.location.search);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const clientId = params.get('client_id') ?? 'roostbot';
  const trusted = FIRST_PARTY_OIDC_CLIENTS.has(clientId);
  const autoStarted = useRef(false);

  async function allow() {
    setBusy(true);
    setError('');
    try {
      const { redirect_url } = await api.authorizeOAuth(session.access_token, {
        response_type: params.get('response_type') ?? 'code',
        client_id: clientId,
        redirect_uri: params.get('redirect_uri') ?? '',
        state: params.get('state') ?? '',
        code_challenge: params.get('code_challenge') ?? '',
        code_challenge_method: params.get('code_challenge_method') ?? 'S256',
        scope: params.get('scope') ?? 'openid email profile',
        nonce: params.get('nonce') ?? '',
      });
      window.location.assign(redirect_url);
    } catch (err) {
      setError(err instanceof Error ? err.message : '授权失败');
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!trusted || autoStarted.current) return;
    autoStarted.current = true;
    void allow();
  }, [trusted]);

  function deny() {
    const redirect = params.get('redirect_uri');
    if (!redirect) return;
    const url = new URL(redirect);
    url.searchParams.set('error', 'access_denied');
    const state = params.get('state');
    if (state) url.searchParams.set('state', state);
    window.location.assign(url.toString());
  }

  if (trusted) {
    return (
      <main className="consent">
        <div className="consent__card">
          <Logo />
          <p className="kicker" style={{ marginTop: 30 }}>FIRST-PARTY HANDOFF</p>
          <h1>正在连接 {clientId}</h1>
          <p className="consent__lead">
            你已登录统一 Roost 组织，正在把同一个身份交给 {clientId}。这是第一方工作台，
            不需要重复确认。
          </p>
          <div className="consent__org">
            <span className="tag tag--live">SIGNED IN</span>
            <span>{session.user.email}</span>
          </div>
          {error && <p className="form-error">{error}</p>}
          <div className="consent__actions">
            <button className="btn btn--ghost" onClick={deny} disabled={busy}>取消</button>
            <button className="btn btn--ink" onClick={allow} disabled={busy}>{busy ? '连接中…' : '重新连接'}</button>
          </div>
          <p className="consent__foot">Roost Cloud · 统一组织身份</p>
        </div>
      </main>
    );
  }
  return (
    <main className="consent">
      <div className="consent__card">
        <Logo />
        <p className="kicker" style={{ marginTop: 30 }}>AUTHORIZE APPLICATION</p>
        <h1>连接 {clientId}</h1>
        <p className="consent__lead">
          {clientId} 请求使用你的 Roost 组织身份登录。它只能拿到你的用户 ID、邮箱和组织
          ID，不能读取支付凭据或直接修改余额。
        </p>
        <div className="consent__org">
          <span className="tag tag--live">SIGNED IN</span>
          <span>{session.user.email}</span>
        </div>
        {error && <p className="form-error">{error}</p>}
        <div className="consent__actions">
          <button className="btn btn--ghost" onClick={deny} disabled={busy}>取消</button>
          <button className="btn btn--ink" onClick={allow} disabled={busy}>{busy ? '授权中…' : '允许访问'}</button>
        </div>
        <p className="consent__foot">Roost Cloud · 统一组织身份</p>
      </div>
    </main>
  );
}

function Console({ session }: { session: Session }) {
  const [me, setMe] = useState<MeResponse | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [pendingOrder, setPendingOrder] = useState<Order | null>(null);
  const token = session.access_token;

  const load = useCallback(async () => {
    try {
      const [meData, plansData, ordersData] = await Promise.all([api.me(token), api.plans(token), api.orders(token)]);
      setMe(meData);
      setPlans(plansData.plans);
      setOrders(ordersData.orders);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败');
    }
  }, [token]);

  useEffect(() => { void load(); }, [load]);

  async function buy(sku: string) {
    setBusy(sku);
    setError('');
    try {
      const { order } = await api.createOrder(token, sku);
      setPendingOrder(order);
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建订单失败');
    } finally {
      setBusy('');
    }
  }

  const paid = useCallback(() => { void load(); }, [load]);

  if (!me) {
    return (
      <div className="loading">
        <Logo />
        <p>{error || '正在装配组织上下文…'}</p>
        {error && <button className="btn btn--ink" onClick={() => void load()}>重试</button>}
      </div>
    );
  }

  return (
    <div className="app">
      <Header email={me.user.email} onSignOut={() => void supabase.auth.signOut()} />
      <main className="page">
        <div className="page__intro">
          <div>
            <p className="kicker">ORGANIZATION</p>
            <h1>{me.org.name}</h1>
          </div>
          <p className="page__intro-note">一个组织 ID，贯穿 Sandbox、Market、dslpha 与 causal-memory。积分与凭证实时共享。</p>
        </div>
        {error && <p className="form-error">{error}</p>}
        <Workspaces />
        <WalletCard billing={me.billing} />
        <Products billing={me.billing} />
        <CausalMemory token={token} orgId={me.org.id} />
        <Plans plans={plans} current={me.billing.plan.code} onBuy={buy} busy={busy} />
        <Orders orders={orders} />
        <footer className="page__foot">
          <span>© 2026 Roost · 统一控制面</span>
          <span>100 credits = ¥1 · 虎皮椒单次支付</span>
        </footer>
      </main>
      {pendingOrder && <OrderModal token={token} order={pendingOrder} onClose={() => setPendingOrder(null)} onPaid={paid} />}
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    void supabase.auth.getSession().then(({ data }) => {
      setSession(data.session);
      setLoading(false);
    });
    const { data: listener } = supabase.auth.onAuthStateChange((_event, next) => setSession(next));
    return () => listener.subscription.unsubscribe();
  }, []);

  const isOAuth = window.location.pathname.includes('/oauth/authorize');
  const view = useMemo(() => {
    if (loading) return <div className="loading"><p>加载中…</p></div>;
    if (isOAuth) return session ? <OAuthAuthorize session={session} /> : <AuthScreen />;
    return session ? <Console session={session} /> : <AuthScreen />;
  }, [isOAuth, loading, session]);

  return view;
}
