package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefInt {
    private Field field;

    public RefInt(Class cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public int get(Object object) {
        try {
            return this.field.getInt(object);
        } catch (Exception e) {
            return 0;
        }
    }

    public void set(Object obj, int intValue) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.setInt(obj, intValue);
        } catch (Exception e) {
        }
    }
}
