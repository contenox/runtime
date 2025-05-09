import { Button, Form, FormField, Input, PasswordInput } from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useRegister } from '../../../hooks/useRegister';

type FormSwitchProps = {
  onSwitch: () => void;
};

export function RegisterForm({ onSwitch }: FormSwitchProps) {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [localError, setLocalError] = useState<string | null>(null);

  const {
    mutate: registerMutate,
    isPending: isRegisterPending,
    error: registerError,
  } = useRegister();

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirmPassword) {
      setLocalError(t('register.password_mismatch', 'Passwords do not match'));
      return;
    }
    setLocalError(null);
    registerMutate({ email, password });
  };

  return (
    <Form
      title={t('register.title')}
      onSubmit={handleSubmit}
      error={
        localError ||
        (registerError
          ? t('register.error', 'Registration error: {{error}}', { error: registerError.message })
          : undefined)
      }
      onError={errorMsg => console.error('Form error:', errorMsg)}
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isRegisterPending}>
            {isRegisterPending ? t('register.loading') : t('register.submit')}
          </Button>
          <Button type="button" variant="secondary" onClick={onSwitch} disabled={isRegisterPending}>
            {t('register.switch_to_login')}
          </Button>
        </>
      }>
      <FormField label={t('register.email')} required>
        <Input
          value={email}
          onChange={e => setEmail(e.target.value)}
          disabled={isRegisterPending}
        />
      </FormField>
      <FormField label={t('register.password')} required>
        <PasswordInput
          value={password}
          onChange={e => setPassword(e.target.value)}
          disabled={isRegisterPending}
        />
      </FormField>
      <FormField label={t('register.confirm_password')} required>
        <PasswordInput
          value={confirmPassword}
          onChange={e => setConfirmPassword(e.target.value)}
          disabled={isRegisterPending}
        />
      </FormField>
    </Form>
  );
}
