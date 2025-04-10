import { Button, Form, FormField, Input, PasswordInput } from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLogin } from '../../../hooks/useLogin';

type FormSwitchProps = {
  onSwitch: () => void;
};

export function LoginForm({ onSwitch }: FormSwitchProps) {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  const { mutate: loginMutate, isPending: isLoginPending, error: loginError } = useLogin();

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    loginMutate({ email, password });
  };

  return (
    <Form
      title={t('login.title')}
      onSubmit={handleSubmit}
      error={
        loginError
          ? t('login.error', 'Login error: {{error}}', { error: loginError.message })
          : undefined
      }
      onError={errorMsg => console.error('Form error:', errorMsg)}
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isLoginPending}>
            {isLoginPending ? t('login.loading') : t('login.submit')}
          </Button>
          <Button type="button" variant="secondary" onClick={onSwitch} disabled={isLoginPending}>
            {t('login.switch_to_register')}
          </Button>
        </>
      }>
      <FormField label={t('login.user_name')} required>
        <Input value={email} onChange={e => setEmail(e.target.value)} disabled={isLoginPending} />
      </FormField>
      <FormField label={t('login.user_password')} required>
        <PasswordInput
          value={password}
          onChange={e => setPassword(e.target.value)}
          disabled={isLoginPending}
        />
      </FormField>
    </Form>
  );
}
