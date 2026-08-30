import React from 'react';
import { MessageSquare } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';

export const CommunityPage: React.FC = () => {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';

  return (
    <section className="product-page-shell empty-product-page" aria-labelledby="community-title">
      <div className="product-page-heading">
        <span>{zh ? '社区' : 'Community'}</span>
        <h1 id="community-title">{zh ? '社区' : 'Community'}</h1>
        <p>{zh ? '社区功能暂未设计。' : 'Community features have not been designed yet.'}</p>
      </div>
      <div className="empty-product-surface">
        <MessageSquare aria-hidden="true" />
        <strong>{zh ? '这里暂时留白' : 'This space is intentionally blank'}</strong>
        <span>{zh ? '后续确定社区功能后再继续建设。' : 'We will build this page after the community scope is defined.'}</span>
      </div>
    </section>
  );
};
