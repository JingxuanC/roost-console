import { useCallback, useEffect, useMemo, useState } from 'react';
import type { Session } from '@supabase/supabase-js';
import { supabase } from './lib/supabase';
import { api, type BillingView, type MeResponse, type Order, type Plan } from './lib/api';

const PRODUCT_LABELS: Record<string, string> = {
  dslpha: 'DeepSeek Alpha · 投研工作台',
  roostbot: 'Roost Bot · 云端 Bot 团队',
  market: 'Roost Market · 算力市场',
};

const CREDIT_PACKS = [
  { sku: 'credit_50', label: '5,000 credits', price: '¥50' },
  { sku: 'credit_100', label: '10,000 credits', price: '¥100' },
  { sku: 'credit_500', label: '50,000 credits', price: '¥500' },
];

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
        const { data, error } = await supabase.auth.signUp({ email, password });
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
          <h1>一个账号，<br /><span>四个产品。</span></h1>
          <p className="auth__lead">
            Roost 组织统一账号、积分和账单。注册之后，dsalpha、roostbot、market
            共用同一份余额，不用再分别充值。
          </p>
          <div className="auth__ledger">
            <div className="auth__ledger-head">
              <span>org_roost</span>
              <span className="tag tag--live">active</span>
            </div>
            <div className="auth__ledger-row"><span>余额</span><strong>12,480 credits</strong></div>
            <div className="auth__ledger-row"><span>产品</span><span>dsalpha · roostbot · market</span></div>
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
          <h2>一个余额，三个产品。</h2>
        </div>
        <p className="section__intro">每个产品共享组织钱包，同时有独立的月度上限，避免一个任务吃光所有积分。</p>
      </div>
      <div className="product-grid">
        {billing.products.map((product) => {
          const cap = product.monthly_cap_credits ?? 0;
          const pct = cap > 0 ? Math.min(100, Math.round((product.used_this_month / cap) * 100)) : 0;
          return (
            <article className="product-card" key={product.product}>
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
            </article>
          );
        })}
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
          <p className="page__intro-note">一个组织 ID，贯穿 dslpha、roostbot 与 market。积分实时共享。</p>
        </div>
        {error && <p className="form-error">{error}</p>}
        <WalletCard billing={me.billing} />
        <Products billing={me.billing} />
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

  const view = useMemo(() => {
    if (loading) return <div className="loading"><p>加载中…</p></div>;
    return session ? <Console session={session} /> : <AuthScreen />;
  }, [loading, session]);

  return view;
}
